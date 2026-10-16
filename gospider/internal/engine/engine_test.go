package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gospider/internal/config"
	"gospider/internal/engine"
	"gospider/internal/extract"
	"gospider/internal/output"
)

func TestAPIPagesAndDetail(t *testing.T) {
	var mu sync.Mutex
	var pages []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/list" {
			var body struct {
				Page int `json:"page"`
				Size int `json:"size"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			pages = append(pages, body.Page)
			mu.Unlock()
			all := []map[string]any{
				{"id": 1, "name": "a"},
				{"id": 2, "name": "b"},
				{"id": 3, "name": "c"},
				{"id": 4, "name": "d"},
				{"id": 5, "name": "e"},
			}
			size := body.Size
			if size <= 0 {
				size = 2
			}
			start := (body.Page - 1) * size
			if start < 0 {
				start = 0
			}
			end := start + size
			if start > len(all) {
				start = len(all)
			}
			if end > len(all) {
				end = len(all)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"total": 5, "list": all[start:end]})
			return
		}
		id := r.URL.Path[len("/item/"):]
		_ = json.NewEncoder(w).Encode(map[string]any{"bio": "bio-" + id})
	}))
	defer srv.Close()

	id, err := extract.NewField("id", "", "", "id", "")
	if err != nil {
		t.Fatal(err)
	}
	name, err := extract.NewField("name", "", "", "name", "")
	if err != nil {
		t.Fatal(err)
	}
	bio, err := extract.NewField("bio", "", "", "bio", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Name:        "stars",
		Mode:        "api",
		Workers:     4,
		Timeout:     2 * time.Second,
		MaxRequests: 100,
		Request: config.Request{
			URL:    srv.URL + "/list",
			Method: "POST",
			Body:   map[string]any{"page": 1, "size": 2},
		},
		Page: config.Page{
			In:        "body",
			Param:     "page",
			Start:     1,
			Size:      2,
			SizeParam: "size",
			Total:     "total",
		},
		List:   "list",
		Fields: []extract.Field{id, name},
		Detail: &config.Detail{
			URL:    srv.URL + "/item/{id}",
			Method: "GET",
			Fields: []extract.Field{bio},
		},
		Output: config.Output{Format: "jsonl", Path: "data/stars.jsonl"},
	}
	items, st, err := run(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 || st.Done != 3 || st.Success != 8 || st.Failed != 0 {
		t.Fatalf("items=%d stats=%+v pages=%v", len(items), st, pages)
	}
	got := map[string]string{}
	for _, it := range items {
		got[extract.Stringify(it["name"])] = extract.Stringify(it["bio"])
	}
	if got["a"] != "bio-1" || got["e"] != "bio-5" || len(got) != 5 {
		t.Fatalf("got=%v", got)
	}
}

func TestAPIStopsWhenEmpty(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		page := 1
		if p := r.URL.Query().Get("page"); p != "" {
			page = atoi(p)
		}
		if page > 2 {
			_ = json.NewEncoder(w).Encode(map[string]any{"list": []any{}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"list": []any{
			map[string]any{"id": page*10 + 1},
			map[string]any{"id": page*10 + 2},
		}})
	}))
	defer srv.Close()

	items, st, err := run(t, apiUnknown(srv.URL+"/list"))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 || st.Done > 8 || n.Load() > 8 {
		t.Fatalf("items=%d done=%d requests=%d", len(items), st.Done, n.Load())
	}
}

func TestAPIStopsOnDuplicatePage(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"list": []any{
			map[string]any{"id": 1, "name": "same"},
			map[string]any{"id": 2, "name": "same"},
		}})
	}))
	defer srv.Close()

	items, _, err := run(t, apiUnknown(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || n.Load() > 6 {
		t.Fatalf("items=%d requests=%d", len(items), n.Load())
	}
}

func TestSiteCrawl(t *testing.T) {
	hits := map[string]int{}
	var mu sync.Mutex
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<div class="card"><a href="/p/1">One</a><span class="price">1</span></div>
<div class="card"><a href="/p/1">One dup</a><span class="price">1</span></div>
<div class="card"><a href="/missing">Gone</a></div>
<a class="next" href="/page/2">next</a>
<a class="out" href="https://evil.test/nope">out</a>`))
		case "/page/2":
			_, _ = w.Write([]byte(`<div class="card"><a href="/p/2">Two</a><span class="price">2</span></div>`))
		case "/p/1":
			_, _ = w.Write([]byte(`<h1>One</h1><p class="desc">d1</p>`))
		case "/p/2":
			_, _ = w.Write([]byte(`<h1>Two</h1><p class="desc">d2</p>`))
		default:
			http.NotFound(w, r)
		}
	})

	title, _ := extract.NewField("title", "a", "text", "", "")
	price, _ := extract.NewField("price", ".price", "text", "", "")
	h1, _ := extract.NewField("title", "h1", "text", "", "")
	desc, _ := extract.NewField("desc", ".desc", "text", "", "")
	cfg := &config.Config{
		Name:        "site",
		Mode:        "site",
		Workers:     1,
		Timeout:     2 * time.Second,
		MaxRequests: 50,
		MaxDepth:    1,
		Seeds:       []config.Seed{{URL: srv.URL + "/", Rule: "list", Method: "GET"}},
		Rules: []config.Rule{
			{
				Name:   "list",
				Item:   ".card",
				Fields: []extract.Field{title, price},
				Links: []config.Link{
					{CSS: ".card a", Attr: "href", Rule: "detail"},
					{CSS: "a.out", Attr: "href", Rule: "detail"},
				},
				Next: &config.Link{CSS: "a.next", Attr: "href"},
			},
			{Name: "detail", Fields: []extract.Field{h1, desc}},
		},
		Output: config.Output{Format: "jsonl"},
	}
	items, st, err := run(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 6 || st.Failed != 1 || st.Success != 4 {
		t.Fatalf("items=%d stats=%+v hits=%v", len(items), st, hits)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits["/p/1"] != 1 || hits["/page/2"] != 1 || hits["/p/2"] != 1 {
		t.Fatalf("hits=%v", hits)
	}
	var details int
	for _, it := range items {
		if extract.Stringify(it["desc"]) != "" {
			details++
		}
	}
	if details != 2 {
		t.Fatalf("details=%d items=%v", details, items)
	}
}

func TestSiteMaxPages(t *testing.T) {
	var page2 atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/page/2" {
			page2.Add(1)
		}
		if r.URL.Path == "/p/1" {
			_, _ = w.Write([]byte(`<h1>One</h1>`))
			return
		}
		_, _ = w.Write([]byte(`<div class="card"><a href="/p/1">One</a></div><a class="next" href="/page/2">n</a>`))
	}))
	defer srv.Close()

	title, _ := extract.NewField("title", "a", "text", "", "")
	h1, _ := extract.NewField("title", "h1", "text", "", "")
	cfg := &config.Config{
		Name:        "site",
		Mode:        "site",
		Workers:     2,
		Timeout:     2 * time.Second,
		MaxRequests: 20,
		MaxPages:    1,
		MaxDepth:    1,
		Seeds:       []config.Seed{{URL: srv.URL + "/", Rule: "list"}},
		Rules: []config.Rule{
			{
				Name:   "list",
				Item:   ".card",
				Fields: []extract.Field{title},
				Links:  []config.Link{{CSS: ".card a", Rule: "detail"}},
				Next:   &config.Link{CSS: "a.next"},
			},
			{Name: "detail", Fields: []extract.Field{h1}},
		},
	}
	items, _, err := run(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if page2.Load() != 0 || len(items) != 2 {
		t.Fatalf("page2=%d items=%d", page2.Load(), len(items))
	}
}

func TestCancel(t *testing.T) {
	var first atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if first.CompareAndSwap(false, true) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"total": 10,
				"list":  []any{map[string]any{"id": 1}},
			})
			return
		}
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
		}
		_, _ = w.Write([]byte(`{"total":10,"list":[]}`))
	}))
	defer srv.Close()

	id, _ := extract.NewField("id", "", "", "id", "")
	cfg := &config.Config{
		Name:    "slow",
		Mode:    "api",
		Workers: 2,
		Timeout: 5 * time.Second,
		Request: config.Request{URL: srv.URL, Method: "GET"},
		Page:    config.Page{In: "query", Param: "page", Start: 1, Size: 1, Total: "total"},
		List:    "list",
		Fields:  []extract.Field{id},
	}
	var buf bytes.Buffer
	out := output.NewBuffer("jsonl", &buf, nil)
	eng, err := engine.New(cfg, out)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = eng.Run(ctx)
	if time.Since(start) > 2*time.Second {
		t.Fatal("cancel did not return")
	}
	if err == nil {
		t.Fatal("expected cancel")
	}
}

func apiUnknown(rawURL string) *config.Config {
	id, _ := extract.NewField("id", "", "", "id", "")
	return &config.Config{
		Name:        "unknown",
		Mode:        "api",
		Workers:     2,
		Timeout:     2 * time.Second,
		MaxRequests: 30,
		Request:     config.Request{URL: rawURL, Method: "GET"},
		Page:        config.Page{In: "query", Param: "page", Start: 1, Size: 2, SizeParam: "size"},
		List:        "list",
		Fields:      []extract.Field{id},
	}
}

func run(t *testing.T, cfg *config.Config) ([]map[string]any, engine.Stats, error) {
	t.Helper()
	var buf bytes.Buffer
	out := output.NewBuffer("jsonl", &buf, cfg.Columns())
	eng, err := engine.New(cfg, out)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	runErr := eng.Run(ctx)
	st := eng.Stats()
	_ = out.Close()
	var items []map[string]any
	for _, line := range strings.Split(buf.String(), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("json %s: %v", line, err)
		}
		items = append(items, m)
	}
	return items, st, runErr
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
