package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"gospider/internal/extract"
)

func (e *Engine) runAPI(ctx context.Context) error {
	start := e.cfg.Page.Start
	items, last, raw, err := e.fetchAPIPage(ctx, start)
	if err != nil {
		return err
	}
	if last > 0 {
		pageTotal := last - start + 1
		if pageTotal < 1 {
			pageTotal = 1
		}
		if e.cfg.MaxPages > 0 && pageTotal > e.cfg.MaxPages {
			pageTotal = e.cfg.MaxPages
			last = start + pageTotal - 1
		}
		e.total.Store(int64(pageTotal))
	}
	if len(items) == 0 {
		e.setErr("没有解析到数据，请检查 list 和 fields")
		return nil
	}
	unknown := last == 0
	if unknown {
		e.remember(raw)
	}
	e.emit(items)

	if last > start {
		for p := start + 1; p <= last; p++ {
			e.enqueue(task{kind: "api", page: p})
		}
	} else if unknown {
		limit := 0
		if e.cfg.MaxPages > 0 {
			limit = start + e.cfg.MaxPages - 1
		}
		hi := start + e.cfg.Workers
		if limit > 0 && hi > limit {
			hi = limit
		}
		if hi >= start+1 {
			e.scan = &scan{unknown: true, last: limit}
			for p := start + 1; p <= hi; p++ {
				e.enqueue(task{kind: "api", page: p})
			}
			e.scan.next.Store(int64(hi))
		}
	}
	return e.finish(ctx)
}

func (e *Engine) execAPI(ctx context.Context, t task) {
	if e.scan != nil && e.scan.halted(t.page) {
		return
	}
	items, _, raw, err := e.fetchAPIPage(ctx, t.page)
	if err != nil {
		return
	}
	if e.scan != nil && e.scan.unknown {
		if e.remember(raw) || len(items) == 0 {
			e.scan.markEmpty(t.page)
			return
		}
	}
	if len(items) == 0 {
		return
	}
	e.emit(items)
	e.scheduleNext()
}

func (e *Engine) scheduleNext() {
	s := e.scan
	if s == nil || !s.unknown || s.stopped() {
		return
	}
	n := int(s.next.Add(1))
	if s.stopped() || s.halted(n) {
		return
	}
	if s.last > 0 && n > s.last {
		return
	}
	e.enqueue(task{kind: "api", page: n})
}

func (s *scan) halted(page int) bool {
	em := s.empty.Load()
	return em > 0 && int64(page) >= em
}

func (s *scan) stopped() bool {
	return s.empty.Load() > 0
}

func (s *scan) markEmpty(page int) {
	for {
		old := s.empty.Load()
		if old > 0 && old <= int64(page) {
			return
		}
		if s.empty.CompareAndSwap(old, int64(page)) {
			return
		}
	}
}

func (e *Engine) fetchAPIPage(ctx context.Context, page int) ([]map[string]any, int, []byte, error) {
	rawURL, body, err := e.build(page)
	if err != nil {
		e.countFail(ctx, e.cfg.Request.URL, err)
		return nil, 0, nil, err
	}
	if err := e.pause(ctx); err != nil {
		return nil, 0, nil, err
	}
	e.setCurrent(rawURL)
	resp, err := e.client.Do(ctx, e.cfg.Request.Method, rawURL, body, e.cfg.Request.Headers)
	if err != nil {
		e.countFail(ctx, rawURL, err)
		return nil, 0, nil, err
	}
	root, err := extract.DecodeJSON(resp.Body)
	if err != nil {
		e.countFail(ctx, rawURL, fmt.Errorf("不是 JSON"))
		return nil, 0, resp.Body, err
	}
	e.countOK()
	items := extract.JSONItems(root, e.cfg.List, e.cfg.Fields)
	return items, e.lastPage(root), resp.Body, nil
}

func (e *Engine) lastPage(root any) int {
	pg := e.cfg.Page
	start := pg.Start
	if pg.TotalPages != "" {
		v, ok := extract.Lookup(root, pg.TotalPages)
		if !ok {
			return 0
		}
		n, ok := extract.AsInt(v)
		if !ok || n < start {
			return 0
		}
		return n
	}
	if pg.Total == "" || pg.Size <= 0 {
		return 0
	}
	v, ok := extract.Lookup(root, pg.Total)
	if !ok {
		return 0
	}
	n, ok := extract.AsInt(v)
	if !ok {
		return 0
	}
	if n <= 0 {
		return start
	}
	pages := (n + pg.Size - 1) / pg.Size
	return start + pages - 1
}

func (e *Engine) build(page int) (string, []byte, error) {
	u, err := url.Parse(e.cfg.Request.URL)
	if err != nil {
		return "", nil, err
	}
	pg := e.cfg.Page
	if pg.In == "query" {
		q := u.Query()
		for k, v := range e.cfg.Request.Body {
			q.Set(k, extract.Stringify(v))
		}
		if pg.Param != "" {
			q.Set(pg.Param, strconv.Itoa(page))
		}
		if pg.SizeParam != "" && pg.Size > 0 {
			q.Set(pg.SizeParam, strconv.Itoa(pg.Size))
		}
		u.RawQuery = q.Encode()
		return u.String(), nil, nil
	}
	m := copyMap(e.cfg.Request.Body)
	if m == nil {
		m = map[string]any{}
	}
	if pg.Param != "" {
		m[pg.Param] = page
	}
	if pg.SizeParam != "" && pg.Size > 0 {
		m[pg.SizeParam] = pg.Size
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", nil, err
	}
	return u.String(), b, nil
}
