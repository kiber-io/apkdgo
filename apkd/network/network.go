package network

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kiber-io/apkd/apkd/logging"
)

var reqSeq uint64
var logger = logging.Named("network")
var proxyConfigMu sync.RWMutex
var clientDefaultsMu sync.RWMutex
var sourceHeadersMu sync.RWMutex
var globalProxyURL *url.URL
var sourceProxyURLs = map[string]*url.URL{}
var proxyInsecureSkipVerify bool
var defaultClientTimeout = 30 * time.Second
var defaultRetryPolicy = DefaultRetryPolice()
var sourceHeaderOverrides = map[string]http.Header{}

func nextRequestID() uint64 {
	n := atomic.AddUint64(&reqSeq, 1)
	return n
}

type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

type retryIfCtxKey struct{}
type requestModuleCtxKey struct{}
type requestLoggerCtxKey struct{}
type withoutClientTimeoutCtxKey struct{}

type RetryDecision uint8

const (
	RetryNo RetryDecision = iota
	RetryYes
	RetryDefault
)

type RetryDecider func(req *http.Request, resp *http.Response, err error, attempt int, maxAttempts int) RetryDecision

type RetryPolice struct {
	MaxAttempts int
	Delay       int
	MaxDelay    int
	RetryStatus []int
	RetryIf     RetryDecider
}

func DefaultRetryPolice() *RetryPolice {
	return &RetryPolice{
		MaxAttempts: 10,
		Delay:       1000, // milliseconds
		MaxDelay:    10000,
		RetryStatus: []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout},
	}
}

func cloneRetryPolicy(policy *RetryPolice) *RetryPolice {
	if policy == nil {
		return nil
	}
	cloned := *policy
	cloned.RetryStatus = append([]int(nil), policy.RetryStatus...)
	return &cloned
}

type Client struct {
	doer           Doer
	retry          *RetryPolice
	defaultHeaders http.Header
}

func DefaultClient() *Client {
	return NewHttpClientForSource("", 0, nil)
}

func DefaultClientForSource(sourceName string) *Client {
	return NewHttpClientForSource(sourceName, 0, nil)
}

func NewHttpClient(timeout time.Duration, p *RetryPolice) *Client {
	return NewHttpClientForSource("", timeout, p)
}

func NewHttpClientForSource(sourceName string, timeout time.Duration, p *RetryPolice) *Client {
	if timeout <= 0 {
		timeout = currentClientTimeout()
	}
	if p == nil {
		p = currentRetryPolicy()
	}
	proxyURL := resolveProxyURL(sourceName)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxyURL != nil {
		transport.Proxy = http.ProxyURL(proxyURL)
		if isProxyInsecureSkipVerifyEnabled() {
			if transport.TLSClientConfig == nil {
				transport.TLSClientConfig = &tls.Config{}
			}
			transport.TLSClientConfig.InsecureSkipVerify = true
		}
	} else {
		transport.Proxy = http.ProxyFromEnvironment
	}
	base := &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
	return &Client{
		doer:  base,
		retry: p,
	}
}

func ConfigureProxies(globalProxy string, sourceProxies map[string]string, insecureSkipVerify bool) error {
	parsedGlobalProxy, err := parseProxyURL(globalProxy)
	if err != nil {
		return fmt.Errorf("invalid global proxy: %w", err)
	}
	parsedSourceProxies := make(map[string]*url.URL, len(sourceProxies))
	for sourceName, rawProxyURL := range sourceProxies {
		normalizedSourceName := strings.ToLower(strings.TrimSpace(sourceName))
		if normalizedSourceName == "" {
			return fmt.Errorf("source name cannot be empty in source proxy map")
		}
		parsedSourceProxy, err := parseProxyURL(rawProxyURL)
		if err != nil {
			return fmt.Errorf("invalid proxy for source %s: %w", normalizedSourceName, err)
		}
		if parsedSourceProxy == nil {
			continue
		}
		parsedSourceProxies[normalizedSourceName] = parsedSourceProxy
	}
	proxyConfigMu.Lock()
	if parsedGlobalProxy != nil {
		logger.Logd(fmt.Sprintf("Configured global proxy: %v", parsedGlobalProxy))
	}
	globalProxyURL = parsedGlobalProxy
	if len(parsedSourceProxies) > 0 {
		logger.Logd(fmt.Sprintf("Configured source proxies: %v", parsedSourceProxies))
	}
	sourceProxyURLs = parsedSourceProxies
	if insecureSkipVerify {
		logger.Logd(fmt.Sprintf("Configured proxy insecure skip verify: %v", insecureSkipVerify))
	}
	proxyInsecureSkipVerify = insecureSkipVerify
	proxyConfigMu.Unlock()
	return nil
}

func ResetClientDefaults() {
	clientDefaultsMu.Lock()
	defaultClientTimeout = 30 * time.Second
	defaultRetryPolicy = DefaultRetryPolice()
	clientDefaultsMu.Unlock()
}

func ConfigureClientDefaults(timeout *time.Duration, retryPolicy *RetryPolice) error {
	if timeout != nil && *timeout <= 0 {
		return fmt.Errorf("timeout must be > 0")
	}
	if retryPolicy != nil {
		if retryPolicy.MaxAttempts <= 0 {
			return fmt.Errorf("retry max attempts must be > 0")
		}
		if retryPolicy.Delay < 0 {
			return fmt.Errorf("retry delay must be >= 0")
		}
		if retryPolicy.MaxDelay < 0 {
			return fmt.Errorf("retry max delay must be >= 0")
		}
		for _, retryStatusCode := range retryPolicy.RetryStatus {
			if retryStatusCode < 100 || retryStatusCode > 599 {
				return fmt.Errorf("invalid retry status code %d", retryStatusCode)
			}
		}
	}

	clientDefaultsMu.Lock()
	if timeout != nil {
		defaultClientTimeout = *timeout
	}
	if retryPolicy != nil {
		defaultRetryPolicy = cloneRetryPolicy(retryPolicy)
	}
	clientDefaultsMu.Unlock()
	return nil
}

func ConfigureSourceHeaderOverrides(sourceHeaders map[string]map[string]string) error {
	normalized := make(map[string]http.Header, len(sourceHeaders))
	for sourceName, headers := range sourceHeaders {
		normalizedSourceName := strings.ToLower(strings.TrimSpace(sourceName))
		if normalizedSourceName == "" {
			return fmt.Errorf("source name cannot be empty in source header map")
		}
		normalizedHeaders := make(http.Header, len(headers))
		for headerName, headerValue := range headers {
			normalizedHeaderName := http.CanonicalHeaderKey(strings.TrimSpace(headerName))
			if normalizedHeaderName == "" {
				return fmt.Errorf("header name cannot be empty for source %s", normalizedSourceName)
			}
			normalizedHeaders.Set(normalizedHeaderName, strings.TrimSpace(headerValue))
		}
		normalized[normalizedSourceName] = normalizedHeaders
	}

	sourceHeadersMu.Lock()
	sourceHeaderOverrides = normalized
	sourceHeadersMu.Unlock()
	return nil
}

func ApplySourceHeaderOverrides(sourceName string, baseHeaders http.Header) http.Header {
	resolvedHeaders := make(http.Header, len(baseHeaders))
	for headerName, values := range baseHeaders {
		resolvedHeaders[headerName] = append([]string(nil), values...)
	}
	normalizedSourceName := strings.ToLower(strings.TrimSpace(sourceName))
	if normalizedSourceName == "" {
		return resolvedHeaders
	}

	sourceHeadersMu.RLock()
	sourceOverrides := sourceHeaderOverrides[normalizedSourceName]
	sourceHeadersMu.RUnlock()
	if len(sourceOverrides) == 0 {
		return resolvedHeaders
	}
	for headerName, values := range sourceOverrides {
		resolvedHeaders[headerName] = append([]string(nil), values...)
	}
	return resolvedHeaders
}

func currentClientTimeout() time.Duration {
	clientDefaultsMu.RLock()
	defer clientDefaultsMu.RUnlock()
	return defaultClientTimeout
}

func currentRetryPolicy() *RetryPolice {
	clientDefaultsMu.RLock()
	defer clientDefaultsMu.RUnlock()
	return cloneRetryPolicy(defaultRetryPolicy)
}

func parseProxyURL(rawProxyURL string) (*url.URL, error) {
	normalizedProxyURL := strings.TrimSpace(rawProxyURL)
	if normalizedProxyURL == "" {
		return nil, nil
	}
	proxyURL, err := url.Parse(normalizedProxyURL)
	if err != nil {
		return nil, err
	}
	if proxyURL.Scheme == "" || proxyURL.Host == "" {
		return nil, fmt.Errorf("proxy URL must include scheme and host")
	}
	return proxyURL, nil
}

func resolveProxyURL(sourceName string) *url.URL {
	normalizedSourceName := strings.ToLower(strings.TrimSpace(sourceName))
	proxyConfigMu.RLock()
	defer proxyConfigMu.RUnlock()
	if normalizedSourceName != "" {
		if sourceProxyURL, exists := sourceProxyURLs[normalizedSourceName]; exists {
			return sourceProxyURL
		}
	}
	return globalProxyURL
}

func isProxyInsecureSkipVerifyEnabled() bool {
	proxyConfigMu.RLock()
	defer proxyConfigMu.RUnlock()
	return proxyInsecureSkipVerify
}

func (c *Client) WithDefaultHeaders(headers http.Header) *Client {
	c.defaultHeaders = headers
	return c
}

func (c *Client) WithRetryIf(decider RetryDecider) *Client {
	c.retry.RetryIf = decider
	return c
}

func defaultRetryDecider(retryStatuses []int) RetryDecider {
	return func(_ *http.Request, resp *http.Response, err error, attempt int, maxAttempts int) RetryDecision {
		if attempt >= maxAttempts {
			return RetryNo
		}
		if err != nil {
			if isRetryableTransportError(err) {
				return RetryYes
			}
			return RetryNo
		}
		if slices.Contains(retryStatuses, resp.StatusCode) {
			return RetryYes
		}
		return RetryNo
	}
}

func isRetryableTransportError(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	if errors.Is(err, syscall.ENETUNREACH) || errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETDOWN) {
		return true
	}
	return false
}

func (c *Client) Do(req *http.Request) (*http.Response, error) {
	var lastErr error
	reqId := nextRequestID()
	module := requestModuleFromRequest(req)
	reqLogger := requestLoggerFromRequest(req)
	activeLogger := logger
	if reqLogger != nil {
		activeLogger = reqLogger
	}
	logContext := requestLogContext(reqId, module)
	if c.defaultHeaders != nil {
		for key, values := range c.defaultHeaders {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	}
	activeLogger.Logd(fmt.Sprintf("%s Sending request: %s %s", logContext, req.Method, req.URL.String()))
	defDecider := defaultRetryDecider(c.retry.RetryStatus)
	decider := c.retry.RetryIf
	if reqDecider := retryIfFromRequest(req); reqDecider != nil {
		decider = reqDecider
	} else if decider == nil {
		decider = defaultRetryDecider(c.retry.RetryStatus)
	}
	shouldRetry := func(resp *http.Response, err error, attempt int) bool {
		if decider == nil {
			return defDecider(req, resp, err, attempt, c.retry.MaxAttempts) == RetryYes
		}
		d := decider(req, resp, err, attempt, c.retry.MaxAttempts)
		if d == RetryDefault {
			return defDecider(req, resp, err, attempt, c.retry.MaxAttempts) == RetryYes
		}
		return d == RetryYes
	}
	effectiveDoer := c.doer
	if withoutClientTimeoutFromRequest(req) {
		if baseHTTPClient, ok := c.doer.(*http.Client); ok && baseHTTPClient.Timeout > 0 {
			httpClientWithoutTimeout := *baseHTTPClient
			httpClientWithoutTimeout.Timeout = 0
			effectiveDoer = &httpClientWithoutTimeout
		}
	}

	for attempt := 1; attempt <= c.retry.MaxAttempts; attempt++ {
		resp, err := effectiveDoer.Do(req)
		if err == nil {
			activeLogger.Logd(fmt.Sprintf("%s Received response: %d %s", logContext, resp.StatusCode, http.StatusText(resp.StatusCode)))
		} else {
			activeLogger.Logd(fmt.Sprintf("%s Request error: %v", logContext, err))
		}
		if !shouldRetry(resp, err, attempt) {
			if err != nil {
				return nil, err
			}
			return resp, nil
		}
		if err != nil {
			lastErr = err
		}
		reason := "unknown reason"
		if err != nil {
			reason = fmt.Sprintf("error: %v", err)
		} else if resp != nil {
			reason = fmt.Sprintf("status code: %d", resp.StatusCode)
		}

		delay := backoffWithJitter(c.retry.Delay, c.retry.MaxDelay, attempt)
		activeLogger.Logw(fmt.Sprintf("%s Attempt %d/%d failed with %s, retrying in %v...", logContext, attempt, c.retry.MaxAttempts, reason, delay))
		select {
		case <-time.After(delay):
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}

	return nil, lastErr
}

func (c *Client) Post(url string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

func WithRequestRetryIf(req *http.Request, decider RetryDecider) *http.Request {
	ctx := context.WithValue(req.Context(), retryIfCtxKey{}, decider)
	return req.WithContext(ctx)
}

func WithoutClientTimeout(req *http.Request) *http.Request {
	if req == nil {
		return nil
	}
	ctx := context.WithValue(req.Context(), withoutClientTimeoutCtxKey{}, true)
	return req.WithContext(ctx)
}

func WithModule(ctx context.Context, module string) context.Context {
	return context.WithValue(ctx, requestModuleCtxKey{}, module)
}

func WithLogger(ctx context.Context, reqLogger *logging.Logger) context.Context {
	return context.WithValue(ctx, requestLoggerCtxKey{}, reqLogger)
}

func retryIfFromRequest(req *http.Request) RetryDecider {
	if val := req.Context().Value(retryIfCtxKey{}); val != nil {
		if decider, ok := val.(RetryDecider); ok {
			return decider
		}
	}
	return nil
}

func withoutClientTimeoutFromRequest(req *http.Request) bool {
	if req == nil {
		return false
	}
	if val := req.Context().Value(withoutClientTimeoutCtxKey{}); val != nil {
		if withoutTimeout, ok := val.(bool); ok {
			return withoutTimeout
		}
	}
	return false
}

func requestModuleFromRequest(req *http.Request) string {
	if req == nil {
		return ""
	}
	if val := req.Context().Value(requestModuleCtxKey{}); val != nil {
		if module, ok := val.(string); ok {
			return module
		}
	}
	return ""
}

func requestLoggerFromRequest(req *http.Request) *logging.Logger {
	if req == nil {
		return nil
	}
	if val := req.Context().Value(requestLoggerCtxKey{}); val != nil {
		if reqLogger, ok := val.(*logging.Logger); ok {
			return reqLogger
		}
	}
	return nil
}

func requestLogContext(reqID uint64, module string) string {
	if module == "" {
		return fmt.Sprintf("req-id=%d", reqID)
	}
	return fmt.Sprintf("req-id=%d module=%s", reqID, module)
}

func backoffWithJitter(baseDelay, maxDelay int, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}

	expDelay := baseDelay * (1 << (attempt - 1))
	capped := min(maxDelay, expDelay)
	if capped <= 0 {
		return 0
	}

	return time.Duration(rand.Intn(capped+1)) * time.Millisecond
}

func ReadAndRestoreBody(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("response or response body is nil")
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewBuffer(body))
	return body, nil
}
