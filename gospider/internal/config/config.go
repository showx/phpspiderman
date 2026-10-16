// Package config 读取一份 YAML，描述一个采集任务。
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/andybalholm/cascadia"
	"gopkg.in/yaml.v3"

	"gospider/internal/extract"
)

// Config 是一个任务。mode 为 site（网页）或 api（JSON 接口）。
type Config struct {
	Name         string
	Mode         string
	Workers      int
	Timeout      time.Duration
	Retry        int
	Delay        time.Duration
	Headers      map[string]string
	Proxy        string
	MaxPages     int
	MaxDepth     int
	MaxRequests  int
	AllowDomains []string
	Seeds        []Seed
	Rules        []Rule
	Request      Request
	Page         Page
	List         string
	Fields       []extract.Field
	Detail       *Detail
	Output       Output

	ready bool
}

// Seed 是站点模式的入口。
type Seed struct {
	URL    string
	Rule   string
	Method string
}

// Rule 描述一类页面怎么取字段、怎么翻页、怎么跟链接。
type Rule struct {
	Name   string
	Item   string
	Fields []extract.Field
	Links  []Link
	Next   *Link
}

// Link 是要继续抓的链接。
type Link struct {
	CSS  string
	Attr string
	Rule string
}

// Request 是接口模式的请求。
type Request struct {
	URL     string
	Method  string
	Headers map[string]string
	Body    map[string]any
}

// Page 描述分页。In 为 query 或 body。
// Total 是总条数路径，TotalPages 是最后一页的页码。
type Page struct {
	In         string
	Param      string
	Start      int
	Size       int
	SizeParam  string
	Total      string
	TotalPages string
	startSet   bool
}

// Detail 用列表里的字段拼出详情地址，再把结果合并进同一条记录。
type Detail struct {
	URL    string
	Method string
	List   string
	Fields []extract.Field
}

// Output 是结果文件。Format 为 jsonl、csv 或 stdout。
type Output struct {
	Format string
	Path   string
}

type rawConfig struct {
	Name         string            `yaml:"name"`
	Mode         string            `yaml:"mode"`
	Workers      int               `yaml:"workers"`
	Timeout      string            `yaml:"timeout"`
	Retry        *int              `yaml:"retry"`
	Delay        string            `yaml:"delay"`
	Headers      map[string]string `yaml:"headers"`
	Proxy        string            `yaml:"proxy"`
	MaxPages     int               `yaml:"max_pages"`
	MaxDepth     *int              `yaml:"max_depth"`
	MaxRequests  *int              `yaml:"max_requests"`
	AllowDomains []string          `yaml:"allow_domains"`
	Seeds        []rawSeed         `yaml:"seeds"`
	Rules        []rawRule         `yaml:"rules"`
	Request      rawRequest        `yaml:"request"`
	Page         rawPage           `yaml:"page"`
	List         string            `yaml:"list"`
	Fields       []rawField        `yaml:"fields"`
	Detail       *rawDetail        `yaml:"detail"`
	Output       rawOutput         `yaml:"output"`
}

type rawSeed struct {
	URL    string `yaml:"url"`
	Rule   string `yaml:"rule"`
	Method string `yaml:"method"`
}

type rawRule struct {
	Name   string     `yaml:"name"`
	Item   string     `yaml:"item"`
	Fields []rawField `yaml:"fields"`
	Links  []rawLink  `yaml:"links"`
	Next   *rawLink   `yaml:"next"`
}

type rawLink struct {
	CSS  string `yaml:"css"`
	Attr string `yaml:"attr"`
	Rule string `yaml:"rule"`
}

type rawField struct {
	Name string `yaml:"name"`
	CSS  string `yaml:"css"`
	Attr string `yaml:"attr"`
	JSON string `yaml:"json"`
	Re   string `yaml:"re"`
}

type rawRequest struct {
	URL     string            `yaml:"url"`
	Method  string            `yaml:"method"`
	Headers map[string]string `yaml:"headers"`
	Body    map[string]any    `yaml:"body"`
}

type rawPage struct {
	In         string `yaml:"in"`
	Param      string `yaml:"param"`
	Start      *int   `yaml:"start"`
	Size       int    `yaml:"size"`
	SizeParam  string `yaml:"size_param"`
	Total      string `yaml:"total"`
	TotalPages string `yaml:"total_pages"`
}

type rawDetail struct {
	URL    string     `yaml:"url"`
	Method string     `yaml:"method"`
	List   string     `yaml:"list"`
	Fields []rawField `yaml:"fields"`
}

type rawOutput struct {
	Format string `yaml:"format"`
	Path   string `yaml:"path"`
}

// Load 从 YAML 文件读取任务。
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置: %w", err)
	}
	fallback := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return parse(b, fallback)
}

// Parse 解析 YAML 文本。
func Parse(b []byte) (*Config, error) {
	return parse(b, "")
}

func parse(b []byte, fallbackName string) (*Config, error) {
	var raw rawConfig
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("配置不是合法的 YAML: %w", err)
	}
	name := sanitizeName(raw.Name)
	if name == "" {
		name = sanitizeName(fallbackName)
	}
	if name == "" {
		name = "task"
	}
	cfg := &Config{
		Name:         name,
		Mode:         strings.ToLower(strings.TrimSpace(raw.Mode)),
		Workers:      raw.Workers,
		Headers:      raw.Headers,
		Proxy:        strings.TrimSpace(raw.Proxy),
		MaxPages:     raw.MaxPages,
		AllowDomains: raw.AllowDomains,
		List:         strings.TrimSpace(raw.List),
		Output:       Output{Format: raw.Output.Format, Path: raw.Output.Path},
	}
	if cfg.Name == "" {
		cfg.Name = "task"
	}
	if raw.Timeout != "" {
		d, err := time.ParseDuration(raw.Timeout)
		if err != nil {
			return nil, fmt.Errorf("timeout 无法解析（示例 20s）: %w", err)
		}
		cfg.Timeout = d
	}
	if raw.Delay != "" {
		d, err := time.ParseDuration(raw.Delay)
		if err != nil {
			return nil, fmt.Errorf("delay 无法解析（示例 200ms）: %w", err)
		}
		cfg.Delay = d
	}
	if raw.Retry != nil {
		cfg.Retry = *raw.Retry
	} else {
		cfg.Retry = 2
	}
	if raw.MaxDepth != nil {
		cfg.MaxDepth = *raw.MaxDepth
	} else if cfg.Mode == "site" || cfg.Mode == "" {
		cfg.MaxDepth = 2
	}
	if raw.MaxRequests != nil {
		cfg.MaxRequests = *raw.MaxRequests
	} else {
		cfg.MaxRequests = 100000
	}
	for _, s := range raw.Seeds {
		cfg.Seeds = append(cfg.Seeds, Seed{
			URL:    strings.TrimSpace(s.URL),
			Rule:   strings.TrimSpace(s.Rule),
			Method: strings.ToUpper(strings.TrimSpace(s.Method)),
		})
	}
	for _, r := range raw.Rules {
		rule, err := convertRule(r)
		if err != nil {
			return nil, err
		}
		cfg.Rules = append(cfg.Rules, rule)
	}
	cfg.Request = Request{
		URL:     strings.TrimSpace(raw.Request.URL),
		Method:  strings.ToUpper(strings.TrimSpace(raw.Request.Method)),
		Headers: raw.Request.Headers,
		Body:    raw.Request.Body,
	}
	cfg.Page = Page{
		In:         strings.ToLower(strings.TrimSpace(raw.Page.In)),
		Param:      strings.TrimSpace(raw.Page.Param),
		Size:       raw.Page.Size,
		SizeParam:  strings.TrimSpace(raw.Page.SizeParam),
		Total:      strings.TrimSpace(raw.Page.Total),
		TotalPages: strings.TrimSpace(raw.Page.TotalPages),
	}
	if raw.Page.Start != nil {
		cfg.Page.Start = *raw.Page.Start
		cfg.Page.startSet = true
	}
	fields, err := convertFields(raw.Fields)
	if err != nil {
		return nil, err
	}
	cfg.Fields = fields
	if raw.Detail != nil {
		df, err := convertFields(raw.Detail.Fields)
		if err != nil {
			return nil, err
		}
		cfg.Detail = &Detail{
			URL:    strings.TrimSpace(raw.Detail.URL),
			Method: strings.ToUpper(strings.TrimSpace(raw.Detail.Method)),
			List:   strings.TrimSpace(raw.Detail.List),
			Fields: df,
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func convertRule(r rawRule) (Rule, error) {
	fields, err := convertFields(r.Fields)
	if err != nil {
		return Rule{}, err
	}
	rule := Rule{Name: strings.TrimSpace(r.Name), Item: strings.TrimSpace(r.Item), Fields: fields}
	if rule.Item != "" {
		if _, err := cascadia.Parse(rule.Item); err != nil {
			return Rule{}, fmt.Errorf("规则 %s 的 item 选择器无效: %w", rule.Name, err)
		}
	}
	for _, lk := range r.Links {
		link, err := convertLink(rule.Name, lk, true)
		if err != nil {
			return Rule{}, err
		}
		rule.Links = append(rule.Links, link)
	}
	if r.Next != nil && strings.TrimSpace(r.Next.CSS) != "" {
		link, err := convertLink(rule.Name, *r.Next, false)
		if err != nil {
			return Rule{}, err
		}
		rule.Next = &link
	}
	return rule, nil
}

func convertLink(rule string, lk rawLink, needRule bool) (Link, error) {
	link := Link{CSS: strings.TrimSpace(lk.CSS), Attr: strings.TrimSpace(lk.Attr), Rule: strings.TrimSpace(lk.Rule)}
	if link.CSS == "" {
		return Link{}, fmt.Errorf("规则 %s 的链接缺少 css", rule)
	}
	if _, err := cascadia.Parse(link.CSS); err != nil {
		return Link{}, fmt.Errorf("规则 %s 的选择器无效: %w", rule, err)
	}
	if needRule && link.Rule == "" {
		return Link{}, fmt.Errorf("规则 %s 的 links 需要指定 rule", rule)
	}
	return link, nil
}

func convertFields(raw []rawField) ([]extract.Field, error) {
	out := make([]extract.Field, 0, len(raw))
	for _, f := range raw {
		field, err := extract.NewField(strings.TrimSpace(f.Name), strings.TrimSpace(f.CSS), strings.TrimSpace(f.Attr), strings.TrimSpace(f.JSON), f.Re)
		if err != nil {
			return nil, err
		}
		out = append(out, field)
	}
	return out, nil
}

// Validate 补默认值并检查配置能否运行。
func (c *Config) Validate() error {
	if c.ready {
		return nil
	}
	if err := c.validate(); err != nil {
		return err
	}
	c.ready = true
	return nil
}

func (c *Config) validate() error {
	c.Mode = strings.ToLower(strings.TrimSpace(c.Mode))
	if c.Name == "" {
		c.Name = "task"
	}
	c.Name = sanitizeName(c.Name)
	if c.Workers <= 0 {
		c.Workers = 8
	}
	if c.Timeout <= 0 {
		c.Timeout = 20 * time.Second
	}
	if c.Retry < 0 {
		c.Retry = 0
	}
	if c.Headers == nil {
		c.Headers = map[string]string{}
	}
	if _, ok := c.Headers["User-Agent"]; !ok {
		if _, ok2 := c.Headers["user-agent"]; !ok2 {
			c.Headers["User-Agent"] = "gospider/1.0"
		}
	}
	c.Output.Format = strings.ToLower(strings.TrimSpace(c.Output.Format))
	if c.Output.Format == "" {
		c.Output.Format = "jsonl"
	}
	switch c.Output.Format {
	case "jsonl", "csv", "stdout":
	default:
		return fmt.Errorf("不支持的输出格式 %s（可用 jsonl、csv、stdout）", c.Output.Format)
	}
	if c.Output.Format == "stdout" {
		c.Output.Path = "-"
	} else if strings.TrimSpace(c.Output.Path) == "" {
		ext := "jsonl"
		if c.Output.Format == "csv" {
			ext = "csv"
		}
		c.Output.Path = filepath.Join("data", c.Name+"."+ext)
	}

	switch c.Mode {
	case "site":
		return c.validateSite()
	case "api":
		return c.validateAPI()
	default:
		return fmt.Errorf("mode 必须是 site 或 api")
	}
}

func (c *Config) validateSite() error {
	if c.Detail != nil {
		return fmt.Errorf("站点模式请用 rules 描述详情页")
	}
	if len(c.Seeds) == 0 {
		return fmt.Errorf("站点模式至少需要一个 seeds")
	}
	if len(c.Rules) == 0 {
		return fmt.Errorf("站点模式至少需要一条 rules")
	}
	names := map[string]struct{}{}
	for _, r := range c.Rules {
		if r.Name == "" {
			return fmt.Errorf("规则缺少 name")
		}
		if _, ok := names[r.Name]; ok {
			return fmt.Errorf("规则名称重复: %s", r.Name)
		}
		names[r.Name] = struct{}{}
	}
	for i := range c.Seeds {
		if c.Seeds[i].Method == "" {
			c.Seeds[i].Method = "GET"
		}
		if _, err := httpURL(c.Seeds[i].URL); err != nil {
			return fmt.Errorf("入口地址无效: %w", err)
		}
		if _, ok := names[c.Seeds[i].Rule]; !ok {
			return fmt.Errorf("入口使用了不存在的规则 %s", c.Seeds[i].Rule)
		}
	}
	for _, r := range c.Rules {
		for _, lk := range r.Links {
			if _, ok := names[lk.Rule]; !ok {
				return fmt.Errorf("规则 %s 要跳到不存在的规则 %s", r.Name, lk.Rule)
			}
		}
	}
	if len(c.AllowDomains) == 0 {
		seen := map[string]struct{}{}
		for _, s := range c.Seeds {
			u, _ := url.Parse(s.URL)
			h := strings.ToLower(u.Hostname())
			if h == "" {
				continue
			}
			if _, ok := seen[h]; ok {
				continue
			}
			seen[h] = struct{}{}
			c.AllowDomains = append(c.AllowDomains, h)
		}
	}
	return nil
}

func (c *Config) validateAPI() error {
	if _, err := httpURL(c.Request.URL); err != nil {
		return fmt.Errorf("接口地址无效: %w", err)
	}
	if c.Request.Method == "" {
		if len(c.Request.Body) > 0 && c.Page.In == "body" {
			c.Request.Method = "POST"
		} else if len(c.Request.Body) > 0 && c.Page.In == "" {
			c.Request.Method = "POST"
		} else {
			c.Request.Method = "GET"
		}
	}
	if c.Page.In == "" {
		if c.Request.Method == "POST" {
			c.Page.In = "body"
		} else {
			c.Page.In = "query"
		}
	}
	if c.Page.In != "body" && c.Page.In != "query" {
		return fmt.Errorf("page.in 只能是 query 或 body")
	}
	if c.Page.Param == "" {
		c.Page.Param = "page"
	}
	if !c.Page.startSet {
		if c.Page.Start == 0 {
			c.Page.Start = 1
		}
		c.Page.startSet = true
	}
	if c.Page.Total != "" && c.Page.TotalPages == "" && c.Page.Size <= 0 {
		return fmt.Errorf("填写了 total 时，page.size 必须大于 0")
	}
	if c.Detail != nil {
		if c.Detail.URL == "" {
			return fmt.Errorf("detail 缺少 url")
		}
		if c.Detail.Method == "" {
			c.Detail.Method = "GET"
		}
	}
	return nil
}

func httpURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("%s", raw)
	}
	return u, nil
}

func sanitizeName(name string) string {
	name = strings.TrimSpace(name)
	return strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', 0:
			return '-'
		default:
			return r
		}
	}, name)
}

// Columns 是配置里声明的字段顺序，供 CSV 使用。
func (c *Config) Columns() []string {
	seen := map[string]struct{}{}
	var cols []string
	add := func(fields []extract.Field) {
		for _, f := range fields {
			if f.Name == "" {
				continue
			}
			if _, ok := seen[f.Name]; ok {
				continue
			}
			seen[f.Name] = struct{}{}
			cols = append(cols, f.Name)
		}
	}
	if c.Mode == "api" {
		add(c.Fields)
		if c.Detail != nil {
			add(c.Detail.Fields)
		}
		return cols
	}
	for _, r := range c.Rules {
		add(r.Fields)
	}
	return cols
}
