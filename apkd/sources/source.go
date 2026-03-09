package sources

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/kiber-io/apkd/apkd/network"

	"github.com/vbauerster/mpb/v8"
)

type Source interface {
	MaxParallelsDownloads() int
	Name() string
	FindByPackage(packageName string, versionCode int) (Version, error)
	FindByDeveloper(developerId string) ([]string, error)
	Download(version Version) (io.ReadCloser, error)
}

type BaseSource struct {
	Source
	Net            network.Doer
	DefaultHeaders http.Header
}

type Error struct {
	error
	SourceName  string
	PackageName string
	Err         error
}

func (s *BaseSource) MaxParallelsDownloads() int {
	return 1
}

func (s *BaseSource) FindByDeveloper(developerId string) ([]string, error) {
	return []string{}, nil
}

type FileType string

const (
	APK  FileType = "apk"
	XAPK FileType = "xapk"
)

type Version struct {
	Name        string
	Code        int
	Size        uint64
	Link        string
	PackageName string
	DeveloperId string
	Type        FileType
}

type ProgressReader struct {
	Reader   io.Reader
	Progress *mpb.Bar
}

func (pr *ProgressReader) Read(p []byte) (int, error) {
	n, err := pr.Reader.Read(p)
	pr.Progress.IncrBy(n)
	return n, err
}

type AppNotFoundError struct {
	PackageName string
}

func (e *AppNotFoundError) Error() string {
	return fmt.Sprintf("%s not found", e.PackageName)
}

var sources = make(map[string]Source)
var sourceFactories []SourceFactory

var initializeRegisteredSourcesOnce sync.Once
var initializeRegisteredSourcesErr error

type SourceFactory func() (Source, error)

func RegisterSourceFactory(factory SourceFactory) {
	sourceFactories = append(sourceFactories, factory)
}

func InitializeRegisteredSources() error {
	initializeRegisteredSourcesOnce.Do(func() {
		for i, sourceFactory := range sourceFactories {
			source, err := sourceFactory()
			if err != nil {
				initializeRegisteredSourcesErr = fmt.Errorf("failed to initialize source from factory #%d: %w", i+1, err)
				return
			}
			if err := Register(source); err != nil {
				initializeRegisteredSourcesErr = fmt.Errorf("failed to register source %s: %w", source.Name(), err)
				return
			}
		}
	})

	return initializeRegisteredSourcesErr
}

func Register(s Source) error {
	if _, exists := sources[s.Name()]; exists {
		return fmt.Errorf("source %s is already registered", s.Name())
	}
	if s.Name() != strings.ToLower(s.Name()) {
		return fmt.Errorf("source name %s should be lowercase", s.Name())
	}
	sources[s.Name()] = s
	return nil
}

func GetAll() map[string]Source {
	registry := make(map[string]Source, len(sources))
	for sourceName, source := range sources {
		registry[sourceName] = source
	}
	return registry
}

func readBody(res *http.Response) ([]byte, error) {
	reader, err := unpackResponse(res)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	return body, err
}

func unpackResponse(res *http.Response) (io.ReadCloser, error) {
	switch res.Header.Get("Content-Encoding") {
	case "gzip":
		gzipReader, err := gzip.NewReader(res.Body)
		if err != nil {
			return nil, err
		}
		return gzipReader, nil
	default:
		return res.Body, nil
	}
}

func createResponseReader(httpClient network.Doer, req *http.Request) (io.ReadCloser, error) {
	if httpClient == nil {
		httpClient = network.DefaultClient()
	}
	req = network.WithoutClientTimeout(req)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("error: %s", resp.Status)
	}
	return resp.Body, nil
}

func (s *BaseSource) NewRequest(method, url string, body io.Reader) (*http.Request, error) {
	ctx := network.WithModule(context.Background(), s.Source.Name())
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	return req, nil
}

func (s *BaseSource) Http() network.Doer {
	if s.Net == nil {
		sourceName := ""
		if s.Source != nil {
			sourceName = s.Source.Name()
		}
		s.Net = network.DefaultClientForSource(sourceName)
	}
	return s.Net
}
