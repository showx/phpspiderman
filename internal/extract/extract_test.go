package extract

import (
	"net/url"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestLookupAndItems(t *testing.T) {
	root, err := DecodeJSON([]byte(`{"total":"5","data":{"list":[{"id":10,"name":"甲"},{"id":11}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	v, ok := Lookup(root, "data.list.0.name")
	if !ok || Stringify(v) != "甲" {
		t.Fatalf("lookup = %v %v", v, ok)
	}
	n, ok := AsInt(mustLookup(t, root, "total"))
	if !ok || n != 5 {
		t.Fatalf("total = %d %v", n, ok)
	}
	f, err := NewField("name", "", "", "name", "")
	if err != nil {
		t.Fatal(err)
	}
	items := JSONItems(root, "data.list", []Field{f})
	if len(items) != 1 || Stringify(items[0]["name"]) != "甲" {
		t.Fatalf("items = %#v", items)
	}
	all := JSONItems(root, "data.list", nil)
	if len(all) != 2 || Stringify(all[1]["id"]) != "11" {
		t.Fatalf("all = %#v", all)
	}
}

func TestHTMLRecordsAndLinks(t *testing.T) {
	html := `<html><head><base href="https://ex.com/list/"></head><body>
		<div class="card"><a href="a" title="甲">甲</a><span class="p">1</span></div>
		<div class="card"><a href="b" title="乙">乙</a><span class="p">2</span></div>
		<a class="next" href="p2">next</a>
		<a href="mailto:a@b.c">mail</a>
	</body></html>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := url.Parse("https://ex.com/start")
	title, err := NewField("title", "a", "title", "", "")
	if err != nil {
		t.Fatal(err)
	}
	price, err := NewField("price", ".p", "text", "", `(\d+)`)
	if err != nil {
		t.Fatal(err)
	}
	recs := HTMLRecords(doc, page, ".card", []Field{title, price})
	if len(recs) != 2 || recs[0]["title"] != "甲" || recs[0]["price"] != "1" {
		t.Fatalf("recs = %#v", recs)
	}
	base := DocumentBase(doc, page)
	links := SelectLinks(doc.Selection, base, ".card a", "href")
	if len(links) != 2 || links[0] != "https://ex.com/list/a" {
		t.Fatalf("links = %#v base=%s", links, base)
	}
	next := SelectLinks(doc.Selection, base, "a.next", "")
	if len(next) != 1 || next[0] != "https://ex.com/list/p2" {
		t.Fatalf("next = %#v", next)
	}
}

func TestExpand(t *testing.T) {
	got, err := Expand("https://ex.com/i/{id}", map[string]any{"id": jsonNumber(t, "42")})
	if err != nil || got != "https://ex.com/i/42" {
		t.Fatalf("expand = %s %v", got, err)
	}
	if _, err := Expand("https://ex.com/{nope}", map[string]any{}); err == nil {
		t.Fatal("expected missing field")
	}
}

func mustLookup(t *testing.T, v any, path string) any {
	t.Helper()
	got, ok := Lookup(v, path)
	if !ok {
		t.Fatalf("missing %s", path)
	}
	return got
}

func jsonNumber(t *testing.T, s string) any {
	t.Helper()
	v, err := DecodeJSON([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}
