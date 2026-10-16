package config

import (
	"strings"
	"testing"
	"time"
)

func TestParseAPI(t *testing.T) {
	cfg, err := Parse([]byte(`
name: stars
mode: api
retry: 0
request:
  url: https://example.com/list
  method: POST
  body:
    currentPage: 1
    pageSize: 20
page:
  in: body
  param: currentPage
  size: 20
  size_param: pageSize
  total: total
list: starList
fields:
  - name: userId
  - name: name
detail:
  url: "https://example.com/star/{userId}"
  fields:
    - name: intro
      json: data.intro
output:
  format: jsonl
  path: data/stars.jsonl
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Request.Method != "POST" || cfg.Page.In != "body" || cfg.Page.Start != 1 {
		t.Fatalf("page=%+v method=%s", cfg.Page, cfg.Request.Method)
	}
	if cfg.Retry != 0 || cfg.Timeout != 20*time.Second || cfg.Workers != 8 {
		t.Fatalf("defaults retry=%d timeout=%s workers=%d", cfg.Retry, cfg.Timeout, cfg.Workers)
	}
	if len(cfg.Fields) != 2 || cfg.Detail == nil || cfg.Detail.Method != "GET" {
		t.Fatalf("fields/detail = %+v %+v", cfg.Fields, cfg.Detail)
	}
	cols := cfg.Columns()
	if strings.Join(cols, ",") != "userId,name,intro" {
		t.Fatalf("cols=%v", cols)
	}
}

func TestParseSiteDefaultsDomain(t *testing.T) {
	cfg, err := Parse([]byte(`
mode: site
seeds:
  - url: https://books.example/index.html
    rule: list
rules:
  - name: list
    item: article
    fields:
      - name: title
        css: h1
    next:
      css: a.next
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxDepth != 2 || len(cfg.AllowDomains) != 1 || cfg.AllowDomains[0] != "books.example" {
		t.Fatalf("depth=%d domains=%v", cfg.MaxDepth, cfg.AllowDomains)
	}
}

func TestExplicitZeroStart(t *testing.T) {
	cfg, err := Parse([]byte(`
mode: api
request:
  url: https://example.com/a
page:
  start: 0
  total_pages: 3
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Page.Start != 0 || cfg.Page.Param != "page" || cfg.Request.Method != "GET" {
		t.Fatalf("%+v %+v", cfg.Page, cfg.Request)
	}
}

func TestRejectBadSelector(t *testing.T) {
	_, err := Parse([]byte(`
mode: site
seeds:
  - {url: https://ex.com/, rule: list}
rules:
  - name: list
    fields:
      - {name: t, css: "a[href="}
`))
	if err == nil {
		t.Fatal("expected selector error")
	}
}

func TestExamples(t *testing.T) {
	for _, p := range []string{"../../examples/site.yaml", "../../examples/api.yaml"} {
		if _, err := Load(p); err != nil {
			t.Fatal(p, err)
		}
	}
}
