package network

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"slices"
	"time"

	"github.com/kiber-io/apkd/apkd/logger"
)

type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

type RetryPolice struct {
	MaxAttempts int
	Delay       int
	MaxDelay    int
	RetryStatus []int
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

func (c *Client) Do(req *http.Request) (*http.Response, error) {
	var lastErr error
	if c.defaultHeaders != nil {
		for key, values := range c.defaultHeaders {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	}

	for attempt := 1; attempt <= c.retry.MaxAttempts; attempt++ {
		resp, err := c.doer.Do(req)
		if err == nil && !slices.Contains(c.retry.RetryStatus, resp.StatusCode) {
			return resp, nil
		}

		if err != nil {
			lastErr = err
			if !err.(net.Error).Timeout() || attempt == c.retry.MaxAttempts {
				return nil, err
			}
		} else {
			_ = resp.Body.Close()
			if attempt == c.retry.MaxAttempts {
				return resp, nil
			}
		}

		delay := backoffWithJitter(c.retry.Delay, c.retry.MaxDelay, attempt)
		logger.Logw(fmt.Sprintf("Request failed (attempt %d/%d), retrying in %v: %v", attempt, c.retry.MaxAttempts, delay, lastErr))
		select {
		case <-time.After(delay):
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}

	return nil, lastErr
}

func (c *Client) NewRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	return req, nil
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
