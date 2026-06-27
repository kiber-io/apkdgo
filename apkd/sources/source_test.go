package sources

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kiber-io/apkd/apkd/network"
)

type stubSource struct {
	name string
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

type trackingReadCloser struct {
	io.Reader
	closed   bool
	closeErr error
}

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return r.closeErr
}

func (s stubSource) MaxParallelsDownloads() int                 { return 1 }
func (s stubSource) Name() string                               { return s.name }
func (s stubSource) FindByPackage(string, int) (Version, error) { return Version{}, nil }
func (s stubSource) FindByDeveloper(string) ([]string, error)   { return nil, nil }
func (s stubSource) Download(Version) (*DownloadStream, error) {
	return &DownloadStream{Body: io.NopCloser(strings.NewReader("")), Size: -1}, nil
}

func TestRegisterValidatesNameAndDuplicates(t *testing.T) {
	oldSources := sources
	sources = map[string]Source{}
	t.Cleanup(func() {
		sources = oldSources
	})

	if err := Register(stubSource{name: "fdroid"}); err != nil {
		t.Fatalf("unexpected register error: %v", err)
	}
	if err := Register(stubSource{name: "fdroid"}); err == nil {
		t.Fatalf("expected duplicate register error")
	}

	sources = map[string]Source{}
	if err := Register(stubSource{name: "FDroid"}); err == nil {
		t.Fatalf("expected lowercase validation error")
	}
}

func TestGetAllReturnsCopy(t *testing.T) {
	oldSources := sources
	sources = map[string]Source{
		"fdroid": stubSource{name: "fdroid"},
	}
	t.Cleanup(func() {
		sources = oldSources
	})

	got := GetAll()
	delete(got, "fdroid")
	if _, ok := sources["fdroid"]; !ok {
		t.Fatalf("expected original registry to stay unchanged")
	}
}

func TestUnpackResponseGzip(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte("hello")); err != nil {
		t.Fatalf("unexpected gzip write error: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("unexpected gzip close error: %v", err)
	}

	resp := &http.Response{
		Header: http.Header{"Content-Encoding": []string{"gzip"}},
		Body:   io.NopCloser(bytes.NewReader(buf.Bytes())),
	}

	reader, err := unpackResponse(resp)
	if err != nil {
		t.Fatalf("unexpected unpack error: %v", err)
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("unexpected read error: %v", err)
	}
	if string(body) != "hello" {
		t.Fatalf("expected %q, got %q", "hello", string(body))
	}
}

func TestReadBody(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{},
		Body:   io.NopCloser(strings.NewReader("payload")),
	}
	body, err := readBody(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(body) != "payload" {
		t.Fatalf("expected %q, got %q", "payload", string(body))
	}
}

func TestCreateResponseReader(t *testing.T) {
	doer := doerFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Status:        "200 OK",
			Header:        http.Header{},
			Body:          io.NopCloser(strings.NewReader("ok")),
			ContentLength: -1,
			Request:       req,
		}, nil
	})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com", http.NoBody)
	if err != nil {
		t.Fatalf("unexpected request error: %v", err)
	}
	stream, err := createResponseReader(doer, req)
	if err != nil {
		t.Fatalf("unexpected createResponseReader error: %v", err)
	}
	defer stream.Body.Close()
	body, err := io.ReadAll(stream.Body)
	if err != nil {
		t.Fatalf("unexpected read error: %v", err)
	}
	if string(body) != "ok" {
		t.Fatalf("expected %q, got %q", "ok", string(body))
	}
	if stream.Size != -1 {
		t.Fatalf("expected Size -1 for unknown content-length, got %d", stream.Size)
	}
}

func TestCreateResponseReaderPropagatesContentLength(t *testing.T) {
	const wantSize int64 = 42
	doer := doerFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Status:        "200 OK",
			Header:        http.Header{},
			Body:          io.NopCloser(strings.NewReader("ok")),
			ContentLength: wantSize,
			Request:       req,
		}, nil
	})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com", http.NoBody)
	if err != nil {
		t.Fatalf("unexpected request error: %v", err)
	}
	stream, err := createResponseReader(doer, req)
	if err != nil {
		t.Fatalf("unexpected createResponseReader error: %v", err)
	}
	defer stream.Body.Close()
	if stream.Size != wantSize {
		t.Fatalf("expected Size %d, got %d", wantSize, stream.Size)
	}
}

func TestCreateResponseReaderNon200(t *testing.T) {
	body := &trackingReadCloser{Reader: strings.NewReader("")}
	doer := doerFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusBadGateway,
			Status:        "502 Bad Gateway",
			Header:        http.Header{},
			Body:          body,
			ContentLength: -1,
			Request:       req,
		}, nil
	})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com", http.NoBody)
	if err != nil {
		t.Fatalf("unexpected request error: %v", err)
	}
	if _, err := createResponseReader(doer, req); err == nil {
		t.Fatalf("expected non-200 error")
	}
	if !body.closed {
		t.Fatalf("expected response body to be closed on non-200 response")
	}
}

func TestCreateResponseReaderNon200CloseError(t *testing.T) {
	closeErr := errors.New("close failed")
	body := &trackingReadCloser{
		Reader:   strings.NewReader(""),
		closeErr: closeErr,
	}
	doer := doerFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusBadGateway,
			Status:        "502 Bad Gateway",
			Header:        http.Header{},
			Body:          body,
			ContentLength: -1,
			Request:       req,
		}, nil
	})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com", http.NoBody)
	if err != nil {
		t.Fatalf("unexpected request error: %v", err)
	}
	_, err = createResponseReader(doer, req)
	if err == nil {
		t.Fatalf("expected close error")
	}
	if !errors.Is(err, closeErr) {
		t.Fatalf("expected wrapped close error %q, got %v", closeErr, err)
	}
	if !body.closed {
		t.Fatalf("expected response body close to be attempted")
	}
}

// setupTestProxy reads APKD_TEST_PROXY and configures it as a global proxy.
// Returns true if a proxy was configured. Resets on test cleanup.
func setupTestProxy(t *testing.T) bool {
	t.Helper()
	proxy := os.Getenv("APKD_TEST_PROXY")
	if proxy == "" {
		return false
	}
	if err := network.ConfigureProxies(proxy, nil, true); err != nil {
		t.Fatalf("failed to configure test proxy %q: %v", proxy, err)
	}
	t.Cleanup(func() { _ = network.ConfigureProxies("", nil, false) }) // restore: no proxy, verify SSL
	return true
}

// setClientTimeout overrides the global HTTP client timeout for the duration of the test.
func setClientTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	if err := network.ConfigureClientDefaults(&d, nil); err != nil {
		t.Fatalf("failed to set client timeout: %v", err)
	}
	t.Cleanup(network.ResetClientDefaults)
}
