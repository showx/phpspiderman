# gospider

一个用 Go 写的采集工具。一份 YAML 描述任务，一条命令开始抓，终端里看进度，另开一个命令看有哪些进程还在跑。

只做两件事：抓**网页**，或抓**JSON 接口**。结果写成 JSONL 或 CSV。不需要数据库，也不渲染 JavaScript。

仅供技术研究，请遵守目标站点的规则，不要用于非法用途。

## 马上跑

需要 Go 1.22 以上。在 `gospider` 目录里执行：

```bash
go run ./cmd/gospider run -c examples/api.yaml
```

另一个终端看进程：

```bash
go run ./cmd/gospider ps
```

网页示例先抓 1 页确认字段：

```bash
go run ./cmd/gospider run -c examples/site.yaml -max-pages 1
```

结果在 `data/`。确认没问题后去掉 `-max-pages`，并按需要改 `workers` 和 `delay`。

装到本机：

```bash
go build -o gospider ./cmd/gospider
./gospider run -c examples/api.yaml
```

## 命令

| 命令 | 作用 |
| --- | --- |
| `run -c 任务.yaml` | 开始采集，终端刷新进度 |
| `run -c 任务.yaml -max-pages 1` | 只抓入口那一页，用来试跑 |
| `run -q` | 不刷新画面，大约每 2 秒打一行 |
| `ps` | 正在跑的任务：进程号、进度、成功、失败、条目、速度 |
| `ps -a` | 连同已经结束的任务 |
| `stop 任务编号` | 通知该进程停止，已写入的结果保留 |
| `log 任务编号` | 查看失败请求 |

`ps` 读的是当前目录下的 `.gospider/`。采集和查看要在同一个目录里执行。

采集时按 `Ctrl+C` 会停下来，已经写入的行还在。

画面大致是：

```text
采集  books    任务 a1b2c3d4    进程 12345
状态  运行中    耗时 0:12    速度 4.0/秒
进度  3/10  30%
请求  成功 8    失败 0    条目 20    队列 4
当前  https://books.toscrape.com/catalogue/page-2.html
```

接口如果能算出总页数，进度会显示 `3/10 30%`。网页抓取事先不知道总量，就显示已完成的请求和队列。

## 网页

`examples/site.yaml` 是完整例子。列表页负责翻页和发现链接，详情页负责写字段。

```yaml
name: books
mode: site
workers: 4
timeout: 20s
retry: 2
delay: 200ms
max_depth: 1
seeds:
  - url: https://books.toscrape.com/
    rule: list
rules:
  - name: list
    links:
      - css: article.product_pod h3 a
        rule: detail
    next:
      css: li.next a
  - name: detail
    fields:
      - name: title
        css: .product_main h1
      - name: price
        css: .product_main .price_color
      - name: url
        css: ":url"
output:
  format: jsonl
  path: data/books.jsonl
```

只想要列表、不进详情时，给列表规则加 `item` 和 `fields`，并删掉 `links`：

```yaml
- name: list
  item: article.product_pod
  fields:
    - name: title
      css: h3 a
      attr: title
    - name: price
      css: p.price_color
  next:
    css: li.next a
```

`attr` 省略时取文本。`href` 和 `src` 会补成绝对地址。`css: ":url"` 表示当前页地址。`re` 是正则，有分组时取第一个分组。

默认只跟随入口网址的域名。要放行别的域名再写 `allow_domains`。

## JSON 接口

对应原来 PHP 版的列表接口、分页和字段。`examples/api.yaml` 可以直接跑。POST 接口写成这样：

```yaml
name: star-list
mode: api
workers: 8
timeout: 20s
retry: 2
headers:
  Cookie: ""
proxy: ""   # http://127.0.0.1:7890 或 socks5://127.0.0.1:1080
request:
  url: https://example.com/rest/web/star/list
  method: POST
  body:
    currentPage: 1
    pageSize: 20
page:
  in: body
  param: currentPage
  size: 20
  size_param: pageSize
  total: total          # 总条数；也可以改成 total_pages: data.pages（最后一页的页码）
list: starList          # 列表在 JSON 里的路径，支持 data.list
fields:
  - name: userId        # 不写 json 时，就用 name 当路径
  - name: name
  - name: fansNumber
detail:                 # 可选，抓完合并进同一条记录
  url: "https://example.com/star/{userId}"
  fields:
    - name: intro
      json: data.intro
output:
  format: jsonl
  path: data/stars.jsonl
```

`page.in` 为 `query` 时，`request.body` 会变成查询参数；为 `body` 时作为 JSON 提交。

不写 `total` / `total_pages` 时，会一直翻页，直到某一页是空的，或和前面某一页内容重复。`fields` 留空则把列表里的对象原样保存。

`{userId}` 用来拼详情地址，花括号里的名字必须是上面抽到的字段。

## 常用配置

| 字段 | 含义 | 默认 |
| --- | --- | --- |
| `workers` | 同时发多少请求 | 8 |
| `timeout` | 单次请求超时，如 `20s` | 20s |
| `retry` | 网络错误、429、5xx 的重试次数 | 2 |
| `delay` | 每个 worker 两次请求之间的间隔，如 `200ms` | 0 |
| `headers` | 请求头，Cookie 也写在这里 | 自带 User-Agent |
| `proxy` | `http`、`https` 或 `socks5` 代理 | 无 |
| `max_pages` | 最多多少页，含入口。1 表示不翻页 | 不限制 |
| `max_depth` | 网页从入口往下跟几层链接 | 2 |
| `max_requests` | 最多调度多少次抓取，防止配错后一直跑。写 0 表示不限制 | 100000 |
| `output.format` | `jsonl`、`csv` 或 `stdout` | jsonl |
| `output.path` | 结果文件，每次运行会覆盖 | `data/<名称>.jsonl` |

`delay` 是每个 worker 自己的间隔，所以大致速度是 `workers / delay`。试跑时把 `workers` 调小，字段对了再加大。

页面按声明转成 UTF-8。接口里的长整数会按原文保存，不会变成科学计数法。

## 和原来的 PHP 版

| PHP | gospider |
| --- | --- |
| Swoole 协程 | `workers` 个 goroutine |
| `handleList` 翻页 | `mode: api` 的 `page` |
| `handleContent` 取字段 | `fields` |
| `handleContentDetail` | `detail`，或网页模式里的下一条 `rule` |
| 代理切换 | `proxy` |
| 终端里自己 `echo` 页码 | `run` 的进度，以及 `ps` |

PHP 版的 MySQL / Redis 连接池没有搬过来。结果先落成文件，需要入库时再导入。

## 做不到的事

- 不执行 JavaScript。数据要在 HTML 或 JSON 里。
- 不去重内容本身，只按网址（接口则按页码）跳过重复请求。
- 进度文件只在本机当前目录，不管远程机器。
