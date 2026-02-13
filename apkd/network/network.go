package network

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"slices"
	"sync/atomic"
	"time"

	"github.com/kiber-io/apkd/apkd/logger"
)

var reqSeq uint64

func nextRequestID() string {
	n := atomic.AddUint64(&reqSeq, 1)
	return fmt.Sprintf("req-%d", n)
}

type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

type retryIfCtxKey struct{}

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
		MaxAttempts: 3,
		Delay:       1000, // milliseconds
		MaxDelay:    10000,
		RetryStatus: []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout},
	}
}

type Client struct {
	doer           Doer
	retry          *RetryPolice
	defaultHeaders http.Header
}

func DefaultClient() *Client {
	return NewHttpClient(30*time.Second, DefaultRetryPolice())
}

func NewHttpClient(timeout time.Duration, p *RetryPolice) *Client {
	base := &http.Client{
		Timeout: timeout,
	}
	if p == nil {
		p = DefaultRetryPolice()
	}
	return &Client{
		doer:  base,
		retry: p,
	}
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
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
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

func (c *Client) Do(req *http.Request) (*http.Response, error) {
	var lastErr error
	reqId := nextRequestID()
	if c.defaultHeaders != nil {
		for key, values := range c.defaultHeaders {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	}
	logger.Logd(fmt.Sprintf("[%s] Sending request: %s %s", reqId, req.Method, req.URL.String()))
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

	for attempt := 1; attempt <= c.retry.MaxAttempts; attempt++ {
		resp, err := c.doer.Do(req)
		if err == nil {
			logger.Logd(fmt.Sprintf("[%s] Received response: %d %s", reqId, resp.StatusCode, http.StatusText(resp.StatusCode)))
		} else {
			logger.Logd(fmt.Sprintf("[%s] Request error: %v", reqId, err))
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
		logger.Logw(fmt.Sprintf("[%s] Attempt %d/%d failed with %s, retrying in %v...", reqId, attempt, c.retry.MaxAttempts, reason, delay))
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

func retryIfFromRequest(req *http.Request) RetryDecider {
	if val := req.Context().Value(retryIfCtxKey{}); val != nil {
		if decider, ok := val.(RetryDecider); ok {
			return decider
		}
	}
	return nil
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
