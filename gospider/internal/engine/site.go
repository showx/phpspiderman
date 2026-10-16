package engine

import (
	"bytes"
	"context"
	"net/url"

	"github.com/PuerkitoBio/goquery"

	"gospider/internal/config"
	"gospider/internal/extract"
)

func (e *Engine) execSite(ctx context.Context, t task) {
	if err := e.pause(ctx); err != nil {
		return
	}
	e.setCurrent(t.url)
	resp, err := e.client.Do(ctx, t.method, t.url, nil, nil)
	if err != nil {
		e.countFail(ctx, t.url, err)
		return
	}
	e.countOK()
	pageURL, err := url.Parse(resp.URL)
	if err != nil {
		e.setErr(err.Error())
		return
	}
	body := extract.DecodeHTML(resp.ContentType, resp.Body)
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		e.setErr(t.url + " 页面无法解析")
		return
	}
	rule, ok := e.rules[t.rule]
	if !ok {
		e.setErr("未知规则 " + t.rule)
		return
	}
	if len(rule.Fields) > 0 {
		e.emit(extract.HTMLRecords(doc, pageURL, rule.Item, rule.Fields))
	}
	if ctx.Err() != nil {
		return
	}
	if t.depth < e.cfg.MaxDepth {
		e.follow(doc, pageURL, rule, t.depth+1)
	}
	if rule.Next != nil {
		base := extract.DocumentBase(doc, pageURL)
		links := extract.SelectLinks(doc.Selection, base, rule.Next.CSS, rule.Next.Attr)
		if len(links) > 0 {
			e.enqueue(task{
				kind:     "site",
				url:      links[0],
				method:   "GET",
				rule:     rule.Name,
				depth:    t.depth,
				pageTurn: true,
			})
		}
	}
}

func (e *Engine) follow(doc *goquery.Document, pageURL *url.URL, rule config.Rule, depth int) {
	base := extract.DocumentBase(doc, pageURL)
	for _, lk := range rule.Links {
		for _, u := range extract.SelectLinks(doc.Selection, base, lk.CSS, lk.Attr) {
			e.enqueue(task{
				kind:   "site",
				url:    u,
				method: "GET",
				rule:   lk.Rule,
				depth:  depth,
			})
		}
	}
}
