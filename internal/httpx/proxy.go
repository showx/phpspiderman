package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

// ProxyConfig 描述代理。URL、API、List 只填一种。
// Every 是同一条 IP 连续用多少次请求；0 表示按默认处理。
type ProxyConfig struct {
	URL      string
	API      string
	List     []string
	Every    int
	Scheme   string
	Username string
	Password string
}

// NormalizeProxy 把 ip:port 或完整地址收成代理 URL。没有协议时用 scheme，默认 http。
func NormalizeProxy(raw, scheme, user, pass string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, `"'`)
	if raw == "" {
		return nil, fmt.Errorf("空代理")
	}
	if !strings.Contains(raw, "://") {
		if scheme == "" {
			scheme = "http"
		}
		raw = scheme + "://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("代理地址无效: %s", strings.TrimSpace(raw))
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
		u.Scheme = strings.ToLower(u.Scheme)
	default:
		return nil, fmt.Errorf("不支持的代理协议 %s（可用 http、https、socks5）", u.Scheme)
	}
	if u.User == nil && user != "" {
		u.User = url.UserPassword(user, pass)
	}
	return u, nil
}

func isSocks(u *url.URL) bool {
	return u != nil && (u.Scheme == "socks5" || u.Scheme == "socks5h")
}

func proxyLabel(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// gate 在请求时给出当前代理。HTTP 只挂在 Transport.Proxy，SOCKS 只挂在 DialContext，避免一次请求计两次。
type gate struct {
	mu     sync.Mutex
	api    string
	scheme string
	user   string
	pass   string
	every  int
	list   []*url.URL
	pool   []*url.URL
	idx    int
	used   int
	bad    bool
	cur    *url.URL
	direct *http.Client
	tr     *http.Transport
	notify func(string)
	socks  bool
}

func newGate(px ProxyConfig) (*gate, error) {
	px.URL = strings.TrimSpace(px.URL)
	px.API = strings.TrimSpace(px.API)
	px.Scheme = strings.ToLower(strings.TrimSpace(px.Scheme))
	var list []string
	for _, s := range px.List {
		if s = strings.TrimSpace(s); s != "" {
			list = append(list, s)
		}
	}
	n := 0
	if px.URL != "" {
		n++
	}
	if px.API != "" {
		n++
	}
	if len(list) > 0 {
		n++
	}
	if n == 0 {
		return nil, nil
	}
	if n > 1 {
		return nil, fmt.Errorf("代理只选一种：固定地址、接口，或列表")
	}
	if px.Every < 0 {
		return nil, fmt.Errorf("proxy.every 不能为负")
	}
	if px.Scheme != "" {
		switch px.Scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return nil, fmt.Errorf("不支持的代理协议 %s（可用 http、https、socks5）", px.Scheme)
		}
	}
	scheme := px.Scheme
	if scheme == "" {
		scheme = "http"
	}
	g := &gate{
		api:    px.API,
		scheme: scheme,
		user:   px.Username,
		pass:   px.Password,
		every:  px.Every,
		direct: &http.Client{Timeout: 15 * time.Second},
	}
	if px.URL != "" {
		u, err := NormalizeProxy(px.URL, scheme, px.Username, px.Password)
		if err != nil {
			return nil, err
		}
		g.list = []*url.URL{u}
		g.every = 0
		g.socks = isSocks(u)
		return g, nil
	}
	if len(list) > 0 {
		var urls []*url.URL
		var socks *bool
		for _, raw := range list {
			u, err := NormalizeProxy(raw, scheme, px.Username, px.Password)
			if err != nil {
				return nil, err
			}
			s := isSocks(u)
			if socks == nil {
				socks = &s
			} else if *socks != s {
				return nil, fmt.Errorf("代理列表不要混用 http 和 socks5")
			}
			urls = append(urls, u)
		}
		g.list = urls
		g.socks = isSocks(urls[0])
		if len(urls) == 1 {
			g.every = 0
		} else if g.every == 0 {
			g.every = 20
		}
		return g, nil
	}
	if _, err := url.ParseRequestURI(px.API); err != nil || !(strings.HasPrefix(px.API, "http://") || strings.HasPrefix(px.API, "https://")) {
		return nil, fmt.Errorf("代理接口地址无效")
	}
	if g.every == 0 {
		g.every = 20
	}
	g.socks = scheme == "socks5" || scheme == "socks5h"
	return g, nil
}

func (g *gate) bind(tr *http.Transport) {
	g.tr = tr
	if g.socks {
		// SOCKS 的拨号只在新建连接时发生。轮换时关掉连接复用，every 才按请求次数算。
		if g.every > 0 {
			tr.DisableKeepAlives = true
		}
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			u, err := g.current(ctx)
			if err != nil {
				return nil, err
			}
			d, err := proxy.FromURL(u, proxy.Direct)
			if err != nil {
				return nil, err
			}
			if cd, ok := d.(proxy.ContextDialer); ok {
				return cd.DialContext(ctx, network, addr)
			}
			return d.Dial(network, addr)
		}
		return
	}
	tr.Proxy = func(req *http.Request) (*url.URL, error) {
		return g.current(req.Context())
	}
}

func (g *gate) rotates() bool {
	return g != nil && (g.api != "" || len(g.list) > 1)
}

func (g *gate) label() string {
	if g == nil {
		return ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return proxyLabel(g.cur)
}

func (g *gate) invalidate() {
	if g == nil || !g.rotates() {
		return
	}
	g.mu.Lock()
	g.bad = true
	g.mu.Unlock()
}

func (g *gate) current(ctx context.Context) (*url.URL, error) {
	g.mu.Lock()
	u, note, err := g.pick(ctx)
	fn := g.notify
	g.mu.Unlock()
	if note != "" && fn != nil {
		fn(note)
	}
	return u, err
}

func (g *gate) pick(ctx context.Context) (*url.URL, string, error) {
	if g.cur != nil && !g.bad && (g.every == 0 || g.used < g.every) {
		if g.every > 0 {
			g.used++
		}
		return g.cur, "", nil
	}
	prev := proxyLabel(g.cur)
	if err := g.advance(ctx); err != nil {
		return nil, "", err
	}
	g.bad = false
	if g.every > 0 {
		g.used = 1
	}
	lab := proxyLabel(g.cur)
	if prev != "" && lab != prev && g.tr != nil {
		g.tr.CloseIdleConnections()
	}
	if lab != prev {
		return g.cur, lab, nil
	}
	return g.cur, "", nil
}

func (g *gate) advance(ctx context.Context) error {
	if g.api != "" {
		if len(g.pool) == 0 {
			urls, err := g.fetch(ctx)
			if err != nil {
				return err
			}
			for _, u := range urls {
				if isSocks(u) != g.socks {
					return fmt.Errorf("代理接口返回的协议和 scheme 不一致")
				}
			}
			g.pool = urls
		}
		g.cur = g.pool[0]
		g.pool = g.pool[1:]
		return nil
	}
	if len(g.list) == 0 {
		return fmt.Errorf("没有可用代理")
	}
	g.cur = g.list[g.idx%len(g.list)]
	g.idx++
	return nil
}

func (g *gate) fetch(ctx context.Context) ([]*url.URL, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.api, nil)
	if err != nil {
		return nil, fmt.Errorf("拉取代理失败: %w", err)
	}
	req.Header.Set("User-Agent", "gospider/1.0")
	resp, err := g.direct.Do(req)
	if err != nil {
		return nil, fmt.Errorf("拉取代理失败: %w", err)
	}
	defer resp.Body.Close()
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("拉取代理失败: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("拉取代理失败: HTTP %d", resp.StatusCode)
	}
	urls, err := parseProxyBody(buf, g.scheme, g.user, g.pass)
	if err != nil {
		return nil, err
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("代理接口没有返回地址")
	}
	return urls, nil
}

func parseProxyBody(body []byte, scheme, user, pass string) ([]*url.URL, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil, fmt.Errorf("代理接口返回为空")
	}
	if body[0] == '{' || body[0] == '[' || body[0] == '"' {
		urls, err := parseProxyJSON(body, scheme, user, pass)
		if err == nil {
			return urls, nil
		}
		var syn *json.SyntaxError
		if !errors.As(err, &syn) {
			return nil, err
		}
	}
	return parseProxyText(string(body), scheme, user, pass)
}

func parseProxyJSON(body []byte, scheme, user, pass string) ([]*url.URL, error) {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, err
	}
	raws := collectProxyStrings(v)
	if len(raws) == 0 {
		return nil, fmt.Errorf("代理接口没有返回地址")
	}
	var out []*url.URL
	for _, s := range raws {
		urls, err := parseProxyText(s, scheme, user, pass)
		if err != nil {
			return nil, err
		}
		out = append(out, urls...)
	}
	return out, nil
}

func collectProxyStrings(v any) []string {
	switch t := v.(type) {
	case string:
		if strings.TrimSpace(t) == "" {
			return nil
		}
		return []string{strings.TrimSpace(t)}
	case []any:
		var out []string
		for _, item := range t {
			out = append(out, collectProxyStrings(item)...)
		}
		return out
	case map[string]any:
		if ip, port, ok := ipPort(t); ok {
			return []string{ip + ":" + port}
		}
		for _, key := range []string{"data", "proxy", "url"} {
			if x, ok := t[key]; ok {
				if s := collectProxyStrings(x); len(s) > 0 {
					return s
				}
			}
		}
	}
	return nil
}

func ipPort(m map[string]any) (string, string, bool) {
	ip, _ := m["ip"].(string)
	if ip == "" {
		ip, _ = m["host"].(string)
	}
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return "", "", false
	}
	port := ""
	switch p := m["port"].(type) {
	case string:
		port = strings.TrimSpace(p)
	case float64:
		port = strconv.Itoa(int(p))
	}
	if port == "" {
		return "", "", false
	}
	return ip, port, true
}

func parseProxyText(s, scheme, user, pass string) ([]*url.URL, error) {
	s = strings.NewReplacer(",", "\n", ";", "\n").Replace(s)
	var out []*url.URL
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		u, err := NormalizeProxy(fields[0], scheme, user, pass)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("代理接口没有返回地址")
	}
	return out, nil
}
