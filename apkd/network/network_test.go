package network

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kiber-io/apkd/apkd/logging"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestDefaultRetryDecider(t *testing.T) {
	decider := defaultRetryDecider([]int{http.StatusTooManyRequests})
	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := decider(req, nil, timeoutErr{}, 1, 3); got != RetryYes {
		t.Fatalf("expected RetryYes on timeout error, got %v", got)
	}
	if got := decider(req, &http.Response{StatusCode: http.StatusTooManyRequests}, nil, 1, 3); got != RetryYes {
		t.Fatalf("expected RetryYes on retry status, got %v", got)
	}
	if got := decider(req, &http.Response{StatusCode: http.StatusOK}, nil, 1, 3); got != RetryNo {
		t.Fatalf("expected RetryNo on non-retry status, got %v", got)
	}
	if got := decider(req, nil, timeoutErr{}, 3, 3); got != RetryNo {
		t.Fatalf("expected RetryNo when attempts are exhausted, got %v", got)
	}
}

func TestWithRequestRetryIf(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	custom := func(_ *http.Request, _ *http.Response, _ error, _ int, _ int) RetryDecision {
		return RetryNo
	}

	req = WithRequestRetryIf(req, custom)
	got := retryIfFromRequest(req)
	if got == nil {
		t.Fatalf("expected retry decider in request context")
	}
	if got(req, nil, nil, 1, 3) != RetryNo {
		t.Fatalf("expected custom decider result")
	}
}

func TestRequestContextHelpers(t *testing.T) {
	ctx := context.Background()
	ctx = WithModule(ctx, "fdroid")
	reqLogger := logging.Named("req-test")
	ctx = WithLogger(ctx, reqLogger)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := requestModuleFromRequest(req); got != "fdroid" {
		t.Fatalf("expected module %q, got %q", "fdroid", got)
	}
	if got := requestLoggerFromRequest(req); got != reqLogger {
		t.Fatalf("expected logger pointer from context")
	}
	if got := requestModuleFromRequest(nil); got != "" {
		t.Fatalf("expected empty module for nil request, got %q", got)
	}
	if got := requestLoggerFromRequest(nil); got != nil {
		t.Fatalf("expected nil logger for nil request")
	}
}

func TestRequestLogContext(t *testing.T) {
	if got := requestLogContext(42, ""); got != "req-id=42" {
		t.Fatalf("unexpected context without module: %q", got)
	}
	if got := requestLogContext(42, "apkcombo"); got != "req-id=42 module=apkcombo" {
		t.Fatalf("unexpected context with module: %q", got)
	}
}

func TestBackoffWithJitter(t *testing.T) {
	for i := 0; i < 20; i++ {
		d := backoffWithJitter(100, 150, 3) // expDelay=400 -> capped=150
		if d < 0 || d > 150*time.Millisecond {
			t.Fatalf("expected delay in [0,150ms], got %v", d)
		}
	}

	d := backoffWithJitter(200, 500, 0) // attempt<1 should be treated as 1
	if d < 0 || d > 200*time.Millisecond {
		t.Fatalf("expected delay in [0,200ms] for attempt<1, got %v", d)
	}

	if d := backoffWithJitter(100, 0, 1); d != 0 {
		t.Fatalf("expected zero delay when capped delay is non-positive, got %v", d)
	}
}

func TestReadAndRestoreBody(t *testing.T) {
	resp := &http.Response{
		Body: io.NopCloser(strings.NewReader("payload")),
	}

	body, err := ReadAndRestoreBody(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(body) != "payload" {
		t.Fatalf("expected body %q, got %q", "payload", string(body))
	}

	restored, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("unexpected read error: %v", err)
	}
	if string(restored) != "payload" {
		t.Fatalf("expected restored body %q, got %q", "payload", string(restored))
	}
}

func TestReadAndRestoreBodyNilResponse(t *testing.T) {
	if _, err := ReadAndRestoreBody(nil); err == nil {
		t.Fatalf("expected error for nil response")
	}
	if _, err := ReadAndRestoreBody(&http.Response{}); err == nil {
		t.Fatalf("expected error for nil response body")
	}
}

func TestConfigureProxies(t *testing.T) {
	proxyConfigMu.Lock()
	oldGlobalProxyURL := globalProxyURL
	oldSourceProxyURLs := sourceProxyURLs
	proxyConfigMu.Unlock()
	t.Cleanup(func() {
		proxyConfigMu.Lock()
		globalProxyURL = oldGlobalProxyURL
		sourceProxyURLs = oldSourceProxyURLs
		proxyConfigMu.Unlock()
	})

	if err := ConfigureProxies("http://127.0.0.1:8080", map[string]string{
		"rustore": "http://127.0.0.1:9090",
	}, false); err != nil {
		t.Fatalf("unexpected configure proxies error: %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("unexpected request error: %v", err)
	}

	rustoreClient := DefaultClientForSource("rustore")
	rustoreHTTPClient, ok := rustoreClient.doer.(*http.Client)
	if !ok {
		t.Fatalf("unexpected doer type: %T", rustoreClient.doer)
	}
	rustoreTransport, ok := rustoreHTTPClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("unexpected transport type: %T", rustoreHTTPClient.Transport)
	}
	rustoreProxyURL, err := rustoreTransport.Proxy(req)
	if err != nil {
		t.Fatalf("unexpected rustore proxy resolve error: %v", err)
	}
	if rustoreProxyURL == nil || rustoreProxyURL.String() != "http://127.0.0.1:9090" {
		t.Fatalf("unexpected rustore proxy URL: %v", rustoreProxyURL)
	}

	fdroidClient := DefaultClientForSource("fdroid")
	fdroidHTTPClient, ok := fdroidClient.doer.(*http.Client)
	if !ok {
		t.Fatalf("unexpected doer type: %T", fdroidClient.doer)
	}
	fdroidTransport, ok := fdroidHTTPClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("unexpected transport type: %T", fdroidHTTPClient.Transport)
	}
	fdroidProxyURL, err := fdroidTransport.Proxy(req)
	if err != nil {
		t.Fatalf("unexpected fdroid proxy resolve error: %v", err)
	}
	if fdroidProxyURL == nil || fdroidProxyURL.String() != "http://127.0.0.1:8080" {
		t.Fatalf("unexpected fdroid proxy URL: %v", fdroidProxyURL)
	}
}

func TestConfigureProxiesInvalidURL(t *testing.T) {
	if err := ConfigureProxies("://bad", nil, false); err == nil {
		t.Fatalf("expected error for invalid global proxy URL")
	}
	if err := ConfigureProxies("", map[string]string{"fdroid": "://bad"}, false); err == nil {
		t.Fatalf("expected error for invalid source proxy URL")
	}
}

func TestConfigureProxiesWithInsecureSkipVerify(t *testing.T) {
	proxyConfigMu.Lock()
	oldGlobalProxyURL := globalProxyURL
	oldSourceProxyURLs := sourceProxyURLs
	oldProxyInsecureSkipVerify := proxyInsecureSkipVerify
	proxyConfigMu.Unlock()
	t.Cleanup(func() {
		proxyConfigMu.Lock()
		globalProxyURL = oldGlobalProxyURL
		sourceProxyURLs = oldSourceProxyURLs
		proxyInsecureSkipVerify = oldProxyInsecureSkipVerify
		proxyConfigMu.Unlock()
	})

	if err := ConfigureProxies("http://127.0.0.1:8080", nil, true); err != nil {
		t.Fatalf("unexpected configure proxies error: %v", err)
	}
	client := DefaultClientForSource("fdroid")
	httpClient, ok := client.doer.(*http.Client)
	if !ok {
		t.Fatalf("unexpected doer type: %T", client.doer)
	}
	transport, ok := httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("unexpected transport type: %T", httpClient.Transport)
	}
	if transport.TLSClientConfig == nil || !transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("expected InsecureSkipVerify=true when proxy insecure mode is enabled")
	}
}

func TestConfigureProxiesWithoutInsecureSkipVerify(t *testing.T) {
	proxyConfigMu.Lock()
	oldGlobalProxyURL := globalProxyURL
	oldSourceProxyURLs := sourceProxyURLs
	oldProxyInsecureSkipVerify := proxyInsecureSkipVerify
	proxyConfigMu.Unlock()
	t.Cleanup(func() {
		proxyConfigMu.Lock()
		globalProxyURL = oldGlobalProxyURL
		sourceProxyURLs = oldSourceProxyURLs
		proxyInsecureSkipVerify = oldProxyInsecureSkipVerify
		proxyConfigMu.Unlock()
	})

	if err := ConfigureProxies("http://127.0.0.1:8080", nil, false); err != nil {
		t.Fatalf("unexpected configure proxies error: %v", err)
	}
	client := DefaultClientForSource("fdroid")
	httpClient, ok := client.doer.(*http.Client)
	if !ok {
		t.Fatalf("unexpected doer type: %T", client.doer)
	}
	transport, ok := httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("unexpected transport type: %T", httpClient.Transport)
	}
	if transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("expected InsecureSkipVerify=false when proxy insecure mode is disabled")
	}
}
