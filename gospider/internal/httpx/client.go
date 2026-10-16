// Package httpx 是带重试和代理的请求客户端。
package httpx

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
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
type Client struct {
	hc      *http.Client
	headers map[string]string
	retry   int
}

// New 创建客户端。proxyURL 支持 http、https、socks5。
func New(timeout time.Duration, retry int, proxyURL string, headers map[string]string, workers int) (*Client, error) {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	if retry < 0 {
		retry = 0
	}
	if workers < 1 {
		workers = 1
	}
	tr, err := transport(proxyURL, workers)
	if err != nil {
		return nil, err
	}
	return &Client{
		hc:      &http.Client{Timeout: timeout, Transport: tr},
		headers: headers,
		retry:   retry,
	}, nil
}

func transport(proxyURL string, workers int) (*http.Transport, error) {
	tr := &http.Transport{
		Proxy:               nil,
		MaxIdleConns:        max(workers*2, 16),
		MaxIdleConnsPerHost: max(workers, 8),
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	if proxyURL == "" {
		return tr, nil
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("代理地址无效: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		tr.Proxy = http.ProxyURL(u)
	case "socks5", "socks5h":
		d, err := proxy.FromURL(u, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("代理地址无效: %w", err)
		}
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if cd, ok := d.(proxy.ContextDialer); ok {
				return cd.DialContext(ctx, network, addr)
			}
			return d.Dial(network, addr)
		}
	default:
		return nil, fmt.Errorf("不支持的代理协议 %s（可用 http、https、socks5）", u.Scheme)
	}
	return tr, nil
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
		} else if !retryStatus(resp.Status) {
			if resp.Status >= 400 {
				return resp, fmt.Errorf("HTTP %d", resp.Status)
			}
			return resp, nil
		} else {
			last = fmt.Errorf("HTTP %d", resp.Status)
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

func retryStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
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
