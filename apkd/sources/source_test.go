package sources

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"
)

type stubSource struct {
	name string
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func (s stubSource) MaxParallelsDownloads() int                 { return 1 }
func (s stubSource) Name() string                               { return s.name }
func (s stubSource) FindByPackage(string, int) (Version, error) { return Version{}, nil }
func (s stubSource) FindByDeveloper(string) ([]string, error)   { return nil, nil }
func (s stubSource) Download(Version) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
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
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("ok")),
			Request:    req,
		}, nil
	})

	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("unexpected request error: %v", err)
	}
	reader, err := createResponseReader(doer, req)
	if err != nil {
		t.Fatalf("unexpected createResponseReader error: %v", err)
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("unexpected read error: %v", err)
	}
	if string(body) != "ok" {
		t.Fatalf("expected %q, got %q", "ok", string(body))
	}
}

func TestCreateResponseReaderNon200(t *testing.T) {
	doer := doerFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Status:     "502 Bad Gateway",
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}, nil
	})

	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("unexpected request error: %v", err)
	}
	if _, err := createResponseReader(doer, req); err == nil {
		t.Fatalf("expected non-200 error")
	}
}
