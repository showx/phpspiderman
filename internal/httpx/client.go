// Package httpx 是带重试和代理的请求客户端。
package httpx

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxBody = 8 << 20

// Response 是一次请求的结果。
type Response struct {
	URL         string
	Status      int
	Body        []byte
	ContentType string
}

// Client 复用连接，并在网络错误、429、5xx 时重试。
// 代理池还会在这些失败，以及 403、407 时丢掉当前 IP。
type Client struct {
	hc      *http.Client
	headers map[string]string
	retry   int
	px      *gate
}

// New 创建客户端。px 为空则直连。
func New(timeout time.Duration, retry int, px ProxyConfig, headers map[string]string, workers int) (*Client, error) {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	if retry < 0 {
		retry = 0
	}
	if workers < 1 {
		workers = 1
	}
	g, err := newGate(px)
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{
		MaxIdleConns:        max(workers*2, 16),
		MaxIdleConnsPerHost: max(workers, 8),
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	if g != nil {
		g.bind(tr)
	}
	return &Client{
		hc:      &http.Client{Timeout: timeout, Transport: tr},
		headers: headers,
		retry:   retry,
		px:      g,
	}, nil
}

// Proxy 返回当前代理，不含账号密码。还没取到时为空。
func (c *Client) Proxy() string {
	if c == nil || c.px == nil {
		return ""
	}
	return c.px.label()
}

// OnSwitch 在换到另一条代理时调用，参数不含账号密码。
func (c *Client) OnSwitch(fn func(string)) {
	if c == nil || c.px == nil {
		return
	}
	c.px.mu.Lock()
	c.px.notify = fn
	c.px.mu.Unlock()
}

// Do 发起请求。body 为 nil 时不带请求体。extra 覆盖默认请求头。
func (c *Client) Do(ctx context.Context, method, rawURL string, body []byte, extra map[string]string) (*Response, error) {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		method = http.MethodGet
	}
	var last error
	attempts := c.retry + 1
	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resp, err := c.once(ctx, method, rawURL, body, extra)
		if err != nil {
			last = err
			c.dropProxy()
		} else if !c.shouldRetry(resp.Status) {
			if resp.Status >= 400 {
				return resp, fmt.Errorf("HTTP %d", resp.Status)
			}
			return resp, nil
		} else {
			last = fmt.Errorf("HTTP %d", resp.Status)
			c.dropProxy()
		}
		if i+1 < attempts {
			if err := sleep(ctx, time.Duration(i+1)*300*time.Millisecond); err != nil {
				return nil, err
			}
		}
	}
	if last == nil {
		last = fmt.Errorf("请求失败")
	}
	return nil, last
}

func (c *Client) dropProxy() {
	if c.px != nil {
		c.px.invalidate()
	}
}

func (c *Client) shouldRetry(code int) bool {
	if code == http.StatusTooManyRequests || code >= 500 {
		return true
	}
	if c.px != nil && c.px.rotates() && (code == http.StatusForbidden || code == http.StatusProxyAuthRequired) {
		return true
	}
	return false
}

func (c *Client) once(ctx context.Context, method, rawURL string, body []byte, extra map[string]string) (*Response, error) {
	var rdr io.Reader
	if body != nil {
		rdr = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
	if err != nil {
		return nil, err
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	buf, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(buf) > maxBody {
		return nil, fmt.Errorf("响应超过 %d 字节", maxBody)
	}
	final := rawURL
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	return &Response{
		URL:         final,
		Status:      resp.StatusCode,
		Body:        buf,
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
