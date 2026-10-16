// Package engine 用固定数量的 worker 并发采集。
package engine

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gospider/internal/config"
	"gospider/internal/extract"
	"gospider/internal/httpx"
	"gospider/internal/output"
)

// Stats 是采集过程中的计数，供界面刷新。
type Stats struct {
	Started                time.Time
	Total, Done            int64
	Success, Failed, Items int64
	Queued                 int64
	Current                string
	LastError              string
}

// Engine 执行一个任务。
type Engine struct {
	cfg      *config.Config
	client   *httpx.Client
	out      *output.Writer
	rules    map[string]config.Rule
	allow    map[string]struct{}
	allowAll bool

	Log func(string)

	started atomic.Int64
	total   atomic.Int64
	done    atomic.Int64
	success atomic.Int64
	failed  atomic.Int64
	items   atomic.Int64
	current atomic.Value
	lastErr atomic.Value

	inflight atomic.Int64
	finished chan struct{}
	once     sync.Once
	wg       sync.WaitGroup
	q        queue

	mu        sync.Mutex
	seen      map[string]struct{}
	bodySeen  map[[32]byte]struct{}
	scheduled int
	pageTurns int
	hitLimit  bool

	scan *scan
}

type scan struct {
	unknown bool
	last    int
	empty   atomic.Int64
	next    atomic.Int64
}

type task struct {
	kind     string
	url      string
	method   string
	rule     string
	depth    int
	page     int
	pageTurn bool
	item     map[string]any
}

// New 按配置准备采集器。
func New(cfg *config.Config, out *output.Writer) (*Engine, error) {
	if cfg == nil {
		return nil, fmt.Errorf("缺少配置")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if out == nil {
		return nil, fmt.Errorf("未设置输出")
	}
	client, err := httpx.New(cfg.Timeout, cfg.Retry, cfg.Proxy, cfg.Headers, cfg.Workers)
	if err != nil {
		return nil, err
	}
	e := &Engine{
		cfg:      cfg,
		client:   client,
		out:      out,
		rules:    map[string]config.Rule{},
		allow:    map[string]struct{}{},
		finished: make(chan struct{}),
		seen:     map[string]struct{}{},
		bodySeen: map[[32]byte]struct{}{},
	}
	e.q.wake = make(chan struct{}, 1)
	e.current.Store("")
	e.lastErr.Store("")
	for _, r := range cfg.Rules {
		e.rules[r.Name] = r
	}
	for _, d := range cfg.AllowDomains {
		if d == "*" {
			e.allowAll = true
		}
		e.allow[strings.ToLower(d)] = struct{}{}
	}
	return e, nil
}

// Run 开始采集，直到队列清空或 ctx 取消。
func (e *Engine) Run(ctx context.Context) error {
	e.started.Store(time.Now().UnixNano())
	switch e.cfg.Mode {
	case "site":
		return e.runSite(ctx)
	case "api":
		return e.runAPI(ctx)
	default:
		return fmt.Errorf("未知模式 %s", e.cfg.Mode)
	}
}

// Stats 返回当前计数。
func (e *Engine) Stats() Stats {
	var started time.Time
	if ns := e.started.Load(); ns > 0 {
		started = time.Unix(0, ns)
	}
	cur, _ := e.current.Load().(string)
	last, _ := e.lastErr.Load().(string)
	return Stats{
		Started:   started,
		Total:     e.total.Load(),
		Done:      e.done.Load(),
		Success:   e.success.Load(),
		Failed:    e.failed.Load(),
		Items:     e.items.Load(),
		Queued:    int64(e.q.len()),
		Current:   cur,
		LastError: last,
	}
}

func (e *Engine) runSite(ctx context.Context) error {
	for _, s := range e.cfg.Seeds {
		e.enqueue(task{
			kind:   "site",
			url:    s.URL,
			method: s.Method,
			rule:   s.Rule,
			depth:  0,
		})
	}
	return e.finish(ctx)
}

func (e *Engine) finish(ctx context.Context) error {
	if e.inflight.Load() == 0 {
		return nil
	}
	e.start(ctx)
	err := e.wait(ctx)
	e.wg.Wait()
	return err
}

func (e *Engine) start(ctx context.Context) {
	n := e.cfg.Workers
	e.wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer e.wg.Done()
			e.loop(ctx)
		}()
	}
}

func (e *Engine) wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.finished:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}
}

func (e *Engine) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.finished:
			return
		case <-e.q.wake:
			t, ok := e.q.pop()
			if !ok {
				continue
			}
			if e.q.len() > 0 {
				e.q.signal()
			}
			e.exec(ctx, t)
		}
	}
}

func (e *Engine) exec(ctx context.Context, t task) {
	defer e.finishOne()
	defer func() {
		if rec := recover(); rec != nil {
			e.setErr(fmt.Sprintf("内部错误: %v", rec))
		}
	}()
	if ctx.Err() != nil {
		return
	}
	switch t.kind {
	case "api":
		e.execAPI(ctx, t)
	case "item":
		e.execItem(ctx, t)
	default:
		e.execSite(ctx, t)
	}
}

func (e *Engine) finishOne() {
	if e.inflight.Add(-1) == 0 {
		e.once.Do(func() { close(e.finished) })
	}
}

func (e *Engine) enqueue(t task) bool {
	if t.method == "" {
		t.method = "GET"
	}
	if t.kind != "item" {
		ok, msg := e.accept(t)
		if msg != "" {
			e.setErr(msg)
		}
		if !ok {
			return false
		}
	}
	e.inflight.Add(1)
	e.q.push(t)
	return true
}

func (e *Engine) accept(t task) (bool, string) {
	if t.kind == "site" {
		u, err := url.Parse(t.url)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return false, ""
		}
		if !e.allowAll && !e.domainOK(u.Hostname()) {
			return false, ""
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	k := t.key()
	if _, ok := e.seen[k]; ok {
		return false, ""
	}
	if e.cfg.MaxRequests > 0 && e.scheduled >= e.cfg.MaxRequests {
		msg := ""
		if !e.hitLimit {
			e.hitLimit = true
			msg = "已达到 max_requests 上限"
		}
		return false, msg
	}
	// max_pages 含入口：1 表示只抓入口，不跟 next。
	if t.pageTurn && e.cfg.MaxPages > 0 && e.pageTurns >= e.cfg.MaxPages-1 {
		return false, ""
	}
	e.seen[k] = struct{}{}
	e.scheduled++
	if t.pageTurn {
		e.pageTurns++
	}
	return true, ""
}

func (t task) key() string {
	if t.kind == "api" {
		return "api|" + strconv.Itoa(t.page)
	}
	return "site|" + t.rule + "|" + t.url
}

func (e *Engine) domainOK(host string) bool {
	_, ok := e.allow[strings.ToLower(host)]
	return ok
}

func (e *Engine) emit(items []map[string]any) {
	for _, item := range items {
		cp := copyMap(item)
		if e.cfg.Detail == nil {
			e.write(cp)
			continue
		}
		e.enqueue(task{kind: "item", item: cp})
	}
}

func (e *Engine) execItem(ctx context.Context, t task) {
	if e.cfg.Detail != nil {
		e.fillDetail(ctx, t.item)
	}
	if ctx.Err() != nil {
		return
	}
	e.write(t.item)
}

func (e *Engine) write(item map[string]any) {
	if err := e.out.Write(item); err != nil {
		e.setErr(err.Error())
		return
	}
	e.items.Add(1)
}

func (e *Engine) fillDetail(ctx context.Context, item map[string]any) {
	d := e.cfg.Detail
	if d == nil {
		return
	}
	if e.cfg.MaxRequests > 0 {
		e.mu.Lock()
		e.scheduled++
		over := e.scheduled > e.cfg.MaxRequests
		e.mu.Unlock()
		if over {
			e.setErr("已达到 max_requests 上限")
			return
		}
	}
	raw, err := extract.Expand(d.URL, item)
	if err != nil {
		e.setErr(err.Error())
		return
	}
	base, _ := url.Parse(e.cfg.Request.URL)
	raw = extract.Resolve(base, raw)
	if raw == "" {
		e.setErr("详情地址无效")
		return
	}
	if err := e.pause(ctx); err != nil {
		return
	}
	e.setCurrent(raw)
	resp, err := e.client.Do(ctx, d.Method, raw, nil, nil)
	if err != nil {
		if ctx.Err() == nil {
			e.failed.Add(1)
			e.setErr(raw + " " + err.Error())
		}
		return
	}
	e.success.Add(1)
	root, err := extract.DecodeJSON(resp.Body)
	if err != nil {
		e.setErr(raw + " 不是 JSON")
		return
	}
	recs := extract.JSONItems(root, d.List, d.Fields)
	if len(recs) == 0 {
		return
	}
	for k, v := range recs[0] {
		item[k] = v
	}
}

func (e *Engine) pause(ctx context.Context) error {
	if e.cfg.Delay <= 0 {
		return nil
	}
	timer := time.NewTimer(e.cfg.Delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (e *Engine) setCurrent(u string) {
	e.current.Store(trim(u, 300))
}

func (e *Engine) setErr(msg string) {
	msg = trim(msg, 300)
	e.lastErr.Store(msg)
	if e.Log != nil && msg != "" {
		e.Log(msg)
	}
}

func (e *Engine) countFail(ctx context.Context, raw string, err error) {
	if ctx.Err() != nil {
		return
	}
	e.done.Add(1)
	e.failed.Add(1)
	e.setErr(raw + " " + err.Error())
}

func (e *Engine) countOK() {
	e.done.Add(1)
	e.success.Add(1)
}

func (e *Engine) remember(body []byte) bool {
	sum := sha256.Sum256(body)
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.bodySeen[sum]; ok {
		return true
	}
	e.bodySeen[sum] = struct{}{}
	return false
}

func trim(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

type queue struct {
	mu    sync.Mutex
	items []task
	head  int
	wake  chan struct{}
}

func (q *queue) push(t task) {
	q.mu.Lock()
	q.items = append(q.items, t)
	q.mu.Unlock()
	q.signal()
}

func (q *queue) pop() (task, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.head >= len(q.items) {
		return task{}, false
	}
	t := q.items[q.head]
	q.items[q.head] = task{}
	q.head++
	if q.head > 1024 && q.head*2 >= len(q.items) {
		q.items = append([]task(nil), q.items[q.head:]...)
		q.head = 0
	}
	return t, true
}

func (q *queue) len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items) - q.head
}

func (q *queue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}
