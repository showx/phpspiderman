// Package extract 从 HTML 和 JSON 里取出字段、链接。
package extract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html/charset"
)

// Field 是一条提取规则。CSS 用于 HTML，JSON 用于接口返回。
// CSS 写成 :url 时，取当前页地址。
type Field struct {
	Name string
	CSS  string
	Attr string
	JSON string
	Re   *regexp.Regexp
}

// NewField 编译正则和 CSS，配置错误在启动时就暴露。
func NewField(name, css, attr, path, re string) (Field, error) {
	if strings.TrimSpace(name) == "" {
		return Field{}, fmt.Errorf("字段缺少 name")
	}
	f := Field{Name: name, CSS: css, Attr: attr, JSON: path}
	if re != "" {
		compiled, err := regexp.Compile(re)
		if err != nil {
			return Field{}, fmt.Errorf("字段 %s 的正则无效: %w", name, err)
		}
		f.Re = compiled
	}
	if css != "" && css != ":url" {
		if _, err := cascadia.Parse(css); err != nil {
			return Field{}, fmt.Errorf("字段 %s 的选择器无效: %w", name, err)
		}
	}
	return f, nil
}

// Apply 按正则取文本。有分组时取第一个分组。
func (f Field) Apply(s string) string {
	s = strings.TrimSpace(s)
	if f.Re == nil || s == "" {
		return s
	}
	m := f.Re.FindStringSubmatch(s)
	switch {
	case len(m) > 1:
		return strings.TrimSpace(m[1])
	case len(m) == 1:
		return strings.TrimSpace(m[0])
	default:
		return ""
	}
}

// Stringify 把 JSON 值转成字符串，整数不带小数。
func Stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return ""
		}
		if t == math.Trunc(t) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprint(t)
		}
		return string(b)
	}
}

// DecodeJSON 解析 JSON，数字保留成 json.Number，避免长整型丢精度。
func DecodeJSON(body []byte) (any, error) {
	body = bytes.TrimPrefix(bytes.TrimSpace(body), []byte{0xEF, 0xBB, 0xBF})
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// DecodeHTML 按页面声明把 HTML 转成 UTF-8。
func DecodeHTML(contentType string, body []byte) []byte {
	r, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return body
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return body
	}
	return out
}

// Lookup 按点路径取值，支持 data.list.0.id。
func Lookup(v any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return v, true
	}
	cur := v
	for _, p := range strings.Split(path, ".") {
		if p == "" {
			return nil, false
		}
		switch n := cur.(type) {
		case map[string]any:
			next, ok := n[p]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(p)
			if err != nil || i < 0 || i >= len(n) {
				return nil, false
			}
			cur = n[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// AsInt 读取页码、总条数这类整数字段。
func AsInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i), true
		}
		f, err := n.Float64()
		if err != nil {
			return 0, false
		}
		return int(f), true
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		return i, err == nil
	default:
		return 0, false
	}
}

// JSONItems 从列表里抽出记录。fields 为空时保留对象的全部字段。
func JSONItems(root any, listPath string, fields []Field) []map[string]any {
	nodes := nodesAt(root, listPath)
	out := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		if len(fields) == 0 {
			m, ok := node.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, copyMap(m))
			continue
		}
		rec := map[string]any{}
		ok := false
		for _, f := range fields {
			path := f.JSON
			if path == "" {
				path = f.Name
			}
			v, found := Lookup(node, path)
			if !found {
				continue
			}
			if f.Re != nil {
				rec[f.Name] = f.Apply(Stringify(v))
			} else {
				rec[f.Name] = v
			}
			ok = true
		}
		if ok {
			out = append(out, rec)
		}
	}
	return out
}

func nodesAt(root any, listPath string) []any {
	v := root
	if listPath != "" {
		found, ok := Lookup(root, listPath)
		if !ok {
			return nil
		}
		v = found
	}
	switch t := v.(type) {
	case []any:
		return t
	case map[string]any:
		return []any{t}
	default:
		return nil
	}
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// DocumentBase 优先用 <base href>。
func DocumentBase(doc *goquery.Document, page *url.URL) *url.URL {
	if doc == nil || page == nil {
		return page
	}
	href, ok := doc.Find("base[href]").First().Attr("href")
	if !ok {
		return page
	}
	u, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return page
	}
	return page.ResolveReference(u)
}

// HTMLRecords 按 item 选择器逐条取字段。item 为空时整页一条。
func HTMLRecords(doc *goquery.Document, page *url.URL, item string, fields []Field) []map[string]any {
	if doc == nil || len(fields) == 0 {
		return nil
	}
	base := DocumentBase(doc, page)
	if item == "" {
		rec := recordFrom(doc.Selection, page, base, fields)
		if rec == nil {
			return nil
		}
		return []map[string]any{rec}
	}
	var out []map[string]any
	doc.Find(item).Each(func(_ int, s *goquery.Selection) {
		if rec := recordFrom(s, page, base, fields); rec != nil {
			out = append(out, rec)
		}
	})
	return out
}

func recordFrom(s *goquery.Selection, page, base *url.URL, fields []Field) map[string]any {
	rec := make(map[string]any, len(fields))
	anyValue := false
	for _, f := range fields {
		var val string
		switch {
		case f.CSS == ":url":
			if page != nil {
				val = page.String()
			}
		default:
			sel := s
			if f.CSS != "" {
				sel = s.Find(f.CSS).First()
			}
			val = nodeValue(sel, f.Attr)
			if f.Attr == "href" || f.Attr == "src" {
				val = Resolve(base, val)
			}
		}
		val = f.Apply(val)
		rec[f.Name] = val
		if val != "" {
			anyValue = true
		}
	}
	if !anyValue {
		return nil
	}
	return rec
}

func nodeValue(s *goquery.Selection, attr string) string {
	if s == nil || s.Length() == 0 {
		return ""
	}
	if attr == "" || attr == "text" {
		return strings.Join(strings.Fields(s.Text()), " ")
	}
	v, _ := s.Attr(attr)
	return strings.TrimSpace(v)
}

// SelectLinks 取出链接并转成绝对地址。attr 为空时用 href。
func SelectLinks(scope *goquery.Selection, base *url.URL, css, attr string) []string {
	if scope == nil || strings.TrimSpace(css) == "" {
		return nil
	}
	if attr == "" {
		attr = "href"
	}
	var out []string
	scope.Find(css).Each(func(_ int, s *goquery.Selection) {
		if u := Resolve(base, nodeValue(s, attr)); u != "" {
			out = append(out, u)
		}
	})
	return out
}

// Resolve 把相对地址补全，并丢掉锚点和脚本链接。
func Resolve(base *url.URL, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.HasPrefix(strings.ToLower(ref), "javascript:") || strings.HasPrefix(strings.ToLower(ref), "mailto:") || strings.HasPrefix(ref, "#") {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	u.Fragment = ""
	return u.String()
}

var placeholder = regexp.MustCompile(`\{([^{}]+)\}`)

// Expand 用记录里的字段替换 {field}。
func Expand(tpl string, item map[string]any) (string, error) {
	var missing []string
	out := placeholder.ReplaceAllStringFunc(tpl, func(m string) string {
		key := m[1 : len(m)-1]
		v, ok := item[key]
		if !ok || v == nil || Stringify(v) == "" {
			missing = append(missing, key)
			return ""
		}
		return url.PathEscape(Stringify(v))
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("详情地址缺少字段 %s", strings.Join(missing, ","))
	}
	return out, nil
}
