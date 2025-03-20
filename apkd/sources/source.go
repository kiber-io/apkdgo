package sources

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/vbauerster/mpb/v8"
)

type Source interface {
	MaxParallelsDownloads() int
	Name() string
	FindByPackage(packageName string, versionCode int64) (Version, error)
	FindByDeveloper(developerId string) ([]string, error)
	Download(version Version) (io.ReadCloser, error)
}

type BaseSource struct{}

type Error struct {
	error
	SourceName  string
	PackageName string
	Err         error
}

func (s BaseSource) MaxParallelsDownloads() int {
	return 1
}

func (s BaseSource) FindByDeveloper(developerId string) ([]string, error) {
	return nil, nil
}

type Version struct {
	Name        string
	Code        int64
	Size        int64
	Link        string
	PackageName string
	DeveloperId string
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

func Register(d Source) {
	if _, exists := sources[d.Name()]; exists {
		fmt.Fprintf(os.Stderr, "Source %s is already registered!\n", d.Name())
		os.Exit(1)
	}
	if d.Name() != strings.ToLower(d.Name()) {
		fmt.Fprintf(os.Stderr, "Source name %s should be lowercase!\n", d.Name())
		os.Exit(1)
	}
	sources[d.Name()] = d
}

func GetAll() map[string]Source {
	return sources
}

func readBody(res *http.Response) ([]byte, error) {
	var reader io.ReadCloser
	var err error
	switch res.Header.Get("Content-Encoding") {
	case "gzip":
		reader, err = gzip.NewReader(res.Body)
		if err != nil {
			return nil, err
		}
		defer reader.Close()
	default:
		reader = res.Body
	}
	body, err := io.ReadAll(reader)
	return body, err
}

func downloadFile(link string) (io.ReadCloser, error) {
	resp, err := http.Get(link)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}
