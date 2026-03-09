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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

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

func TestWithoutClientTimeout(t *testing.T) {
	if req := WithoutClientTimeout(nil); req != nil {
		t.Fatalf("expected nil request to stay nil")
	}

	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if withoutClientTimeoutFromRequest(req) {
		t.Fatalf("expected request without timeout override by default")
	}
	req = WithoutClientTimeout(req)
	if !withoutClientTimeoutFromRequest(req) {
		t.Fatalf("expected request timeout override to be set")
	}
}

func TestDoWithWithoutClientTimeoutDisablesHTTPClientTimeout(t *testing.T) {
	var sawDeadline bool
	baseHTTPClient := &http.Client{
		Timeout: 40 * time.Millisecond,
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			_, sawDeadline = req.Context().Deadline()
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader("ok")),
				Request:    req,
			}, nil
		}),
	}
	client := &Client{
		doer: baseHTTPClient,
		retry: &RetryPolice{
			MaxAttempts: 1,
			Delay:       1,
			MaxDelay:    1,
		},
	}

	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("unexpected request error: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("unexpected request error: %v", err)
	}
	resp.Body.Close()
	if !sawDeadline {
		t.Fatalf("expected request deadline when client timeout is enabled")
	}

	sawDeadline = false
	req, err = http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("unexpected request error: %v", err)
	}
	req = WithoutClientTimeout(req)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("unexpected request error with timeout override: %v", err)
	}
	resp.Body.Close()
	if sawDeadline {
		t.Fatalf("expected request deadline to be disabled by timeout override")
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

func TestConfigureClientDefaults(t *testing.T) {
	clientDefaultsMu.Lock()
	oldTimeout := defaultClientTimeout
	oldRetry := cloneRetryPolicy(defaultRetryPolicy)
	clientDefaultsMu.Unlock()
	t.Cleanup(func() {
		clientDefaultsMu.Lock()
		defaultClientTimeout = oldTimeout
		defaultRetryPolicy = oldRetry
		clientDefaultsMu.Unlock()
	})

	timeout := 42 * time.Second
	retry := &RetryPolice{
		MaxAttempts: 3,
		Delay:       250,
		MaxDelay:    1000,
		RetryStatus: []int{429, 503},
	}
	if err := ConfigureClientDefaults(&timeout, retry); err != nil {
		t.Fatalf("unexpected configure client defaults error: %v", err)
	}
	client := DefaultClientForSource("fdroid")
	httpClient, ok := client.doer.(*http.Client)
	if !ok {
		t.Fatalf("unexpected doer type: %T", client.doer)
	}
	if httpClient.Timeout != timeout {
		t.Fatalf("expected timeout %v, got %v", timeout, httpClient.Timeout)
	}
	if client.retry.MaxAttempts != 3 || client.retry.Delay != 250 || client.retry.MaxDelay != 1000 {
		t.Fatalf("unexpected retry policy: %+v", client.retry)
	}
}

func TestConfigureSourceHeaderOverrides(t *testing.T) {
	sourceHeadersMu.Lock()
	oldOverrides := sourceHeaderOverrides
	sourceHeadersMu.Unlock()
	t.Cleanup(func() {
		sourceHeadersMu.Lock()
		sourceHeaderOverrides = oldOverrides
		sourceHeadersMu.Unlock()
	})

	if err := ConfigureSourceHeaderOverrides(map[string]map[string]string{
		"RuStore": {
			"user-agent": "custom-agent",
			"deviceId":   "custom-id",
		},
	}); err != nil {
		t.Fatalf("unexpected configure source headers error: %v", err)
	}

	headers := ApplySourceHeaderOverrides("rustore", http.Header{
		"User-Agent": {"default-agent"},
		"X-Test":     {"1"},
	})
	if got := headers.Get("User-Agent"); got != "custom-agent" {
		t.Fatalf("expected overridden User-Agent, got %q", got)
	}
	if got := headers.Get("deviceId"); got != "custom-id" {
		t.Fatalf("expected configured deviceId, got %q", got)
	}
	if got := headers.Get("X-Test"); got != "1" {
		t.Fatalf("expected existing header to be preserved, got %q", got)
	}
}
