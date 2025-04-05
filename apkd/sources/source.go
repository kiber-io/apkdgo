package sources

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"kiber-io/apkd/apkd/logger"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/vbauerster/mpb/v8"
)

type Source interface {
	MaxParallelsDownloads() int
	Name() string
	FindByPackage(packageName string, versionCode int) (Version, error)
	FindByDeveloper(developerId string) ([]string, error)
	Download(version Version) (io.ReadCloser, error)
}

type contextKey string

const ctxModuleKey = contextKey("module")

type BaseSource struct {
	Source
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

func Register(s Source) {
	if _, exists := sources[s.Name()]; exists {
		fmt.Fprintf(os.Stderr, "Source %s is already registered!\n", s.Name())
		os.Exit(1)
	}
	if s.Name() != strings.ToLower(s.Name()) {
		fmt.Fprintf(os.Stderr, "Source name %s should be lowercase!\n", s.Name())
		os.Exit(1)
	}
	sources[s.Name()] = s
}

func GetAll() map[string]Source {
	return sources
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

func createResponseReader(req *http.Request) (io.ReadCloser, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("error: %s", resp.Status)
	}
	return resp.Body, nil
}

type LoggingRoundTripper struct {
	original http.RoundTripper
}

func (lrt *LoggingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	moduleName := "unknown"
	if val := req.Context().Value(ctxModuleKey); val != nil {
		moduleName = val.(string)
	}
	requestID := fmt.Sprintf("%s-%d", moduleName, time.Now().UnixNano())
	logger.Logd(fmt.Sprintf("[req %s] Request URL: %s", requestID, req.URL.String()))
	// for name, values := range req.Header {
	// 	for _, v := range values {
	// 		fmt.Printf("%s: %s\n", name, v)
	// 	}
	// }
	// if req.Body != nil {
	// 	bodyBytes, _ := io.ReadAll(req.Body)
	// 	fmt.Println("Body:", string(bodyBytes))
	// 	// Восстанавливаем тело, иначе клиент не сможет отправить его
	// 	req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	// }

	// Выполняем реальный запрос
	resp, err := lrt.original.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	// Логируем ответ
	logger.Logd(fmt.Sprintf("[req %s] Response Status: %s", requestID, resp.Status))
	// for name, values := range resp.Header {
	// 	for _, v := range values {
	// 		fmt.Printf("%s: %s\n", name, v)
	// 	}
	// }
	// if resp.Body != nil {
	// 	bodyBytes, _ := io.ReadAll(resp.Body)
	// 	fmt.Println("Body:", string(bodyBytes))
	// 	resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	// }

	return resp, nil
}

func (s *BaseSource) NewRequest(method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	ctx := context.WithValue(context.Background(), ctxModuleKey, s.Source.Name())
	req = req.WithContext(ctx)
	return req, nil
}

func init() {
	http.DefaultTransport = &LoggingRoundTripper{
		original: http.DefaultTransport,
	}
}
