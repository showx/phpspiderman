# gospider

gospider 是一个命令行采集工具。采集源、运行进度和结果都放在一个 SQLite 文件里，不需要另外装数据库。

它做两件事：按 CSS 抓网页，或按分页抓 JSON 接口。页面要能直接拿到 HTML 或 JSON，不执行 JavaScript。

仅供技术研究。请遵守目标站点的访问规则。

## 安装

需要 Go 1.24 或更高版本。

```bash
go build -o gospider ./cmd/gospider
./gospider help
```

开发时也可以不编译，把下面的 `./gospider` 换成 `go run ./cmd/gospider`。

数据库默认是当前目录的 `gospider.db`。换位置时设置环境变量：

```bash
export GOSPIDER_DB=/var/lib/gospider/gospider.db
```

## 上手

先把示例登记成采集源，再跑：

```bash
./gospider source add -c examples/api.yaml
./gospider source list
./gospider run cat-breeds
```

另一个终端看进程：

```bash
./gospider ps
```

看抓到的记录，或导出成文件：

```bash
./gospider records cat-breeds
./gospider export cat-breeds -o data/breeds.jsonl
```

网页示例先只抓入口页，确认字段再放开：

```bash
./gospider source add -c examples/site.yaml
./gospider run books -max-pages 1
```

`run -c 任务.yaml` 会先登记（或更新）采集源，然后马上开始。`Ctrl+C` 会停掉当前任务，已经写入 SQLite 的记录保留。

## 命令

| 命令 | 作用 |
| --- | --- |
| `source add -c 任务.yaml` | 校验配置，并按其中的 `name` 登记采集源 |
| `source list` | 列出采集源、模式、记录数、最近一次状态 |
| `source show 名称` | 打印这份采集源的 YAML |
| `source rm 名称` | 删除采集源，记录仍留在库里 |
| `source rm 名称 -purge` | 连同记录、任务和错误日志一起删除 |
| `run 名称` | 按已登记的采集源运行 |
| `run -c 任务.yaml` | 登记并立刻运行 |
| `run 名称 -max-pages 1` | 这次只抓入口页，不改已保存的配置 |
| `run -q` | 不刷新画面，大约每 2 秒打一行 |
| `ps` | 正在运行的任务：进程号、进度、成功、失败、条目、速度 |
| `ps -a` | 包含已经结束的任务 |
| `stop 任务编号` | 通知该进程停止 |
| `log 任务编号` | 查看这次运行的错误 |
| `records 名称` | 查看最近记录，默认 20 条，`-n` 可改 |
| `export 名称 -o 文件` | 按采集顺序导出。`.jsonl` 或 `.csv`，也可用 `-f` 指定 |

`ps`、`stop`、`records` 都读同一个 `gospider.db`，所以要和 `run` 使用同一份数据库。

运行时的画面：

```text
采集  books    任务 a1b2c3d4    进程 12345
状态  运行中    耗时 0:12    速度 4.0/秒
进度  3/10  30%
请求  成功 8    失败 0    条目 20    队列 4
当前  https://books.toscrape.com/catalogue/page-2.html
```

接口能算出总页数时，进度是 `3/10 30%`。网页事先不知道总量，就显示已完成的请求和队列长度。

## 结果存在哪里

每条记录都会写入 `gospider.db` 的 `records` 表，这是采集源的正式结果。

配置里的 `output` 是额外的一份文件，方便边跑边看。不需要文件时写成：

```yaml
output:
  format: sqlite
```

库里有四张表：

| 表 | 内容 |
| --- | --- |
| `sources` | 采集源名称、模式、原始 YAML |
| `jobs` | 每次运行的进程和进度 |
| `job_logs` | 失败请求 |
| `records` | 采集结果，一条记录一行 JSON |

可以直接用 SQLite 查询：

```bash
sqlite3 gospider.db "select count(*) from records where source_name='books'"
```

## 网页采集源

`examples/site.yaml` 抓的是 [books.toscrape.com](https://books.toscrape.com/)，一个专门用来练习的站点。列表页负责翻页和发现链接，详情页负责写字段。

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

只要列表、不进详情时，去掉 `links`，改在列表规则上取字段：

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

`attr` 省略时取文本，连续空白会收成一个空格。`href` 和 `src` 会补成绝对地址。`css: ":url"` 表示当前页地址。`re` 是正则，有分组时取第一个分组。

默认只跟随入口网址的域名。要放行其他域名，设置 `allow_domains`。写成 `["*"]` 则不限制域名。

## JSON 接口

`examples/api.yaml` 是一个公开测试接口，登记后可以直接跑。POST 接口写成这样：

```yaml
name: star-list
mode: api
workers: 8
timeout: 20s
retry: 2
headers:
  Cookie: ""
proxy: ""   # 固定地址，或见下方「代理」
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
  total: total
list: starList
fields:
  - name: userId
  - name: name
  - name: fansNumber
detail:
  url: "https://example.com/star/{userId}"
  fields:
    - name: intro
      json: data.intro
output:
  format: sqlite
```

`page.in` 为 `query` 时，`request.body` 会变成查询参数；为 `body` 时作为 JSON 提交。`total` 是总条数。如果接口直接给最后一页的页码，改用 `total_pages`。

不写 `total` 和 `total_pages` 时，会一直翻页，直到某一页是空的，或和前面某一页内容重复。`fields` 留空则把列表里的对象原样保存。

`detail` 用列表里的字段拼地址，`{userId}` 对应上面的字段名。详情结果合并进同一条记录。

数字按原文保存，长整数不会变成科学计数法。

## 配置

| 字段 | 含义 | 默认 |
| --- | --- | --- |
| `name` | 采集源名称，库里的主键 | 配置文件名 |
| `mode` | `site` 或 `api` | 必填 |
| `workers` | 同时发多少请求 | 8 |
| `timeout` | 单次请求超时，如 `20s` | 20s |
| `retry` | 网络错误、429、5xx 的重试次数。写 0 表示不重试 | 2 |
| `delay` | 每个 worker 两次请求之间的间隔，如 `200ms` | 0 |
| `headers` | 请求头。Cookie 也写在这里 | 自带 User-Agent |
| `proxy` | 固定地址，或用 `api` / `list` 轮换 IP | 无 |
| `max_pages` | 最多多少页，含入口。1 表示不翻页 | 不限制 |
| `max_depth` | 网页从入口往下跟几层。翻页不计入深度 | 2 |
| `max_requests` | 最多调度多少次抓取。写 0 表示不限制 | 100000 |
| `allow_domains` | 允许跟随的域名 | 入口网址的域名 |
| `output.format` | `sqlite`、`jsonl`、`csv` 或 `stdout` | jsonl |
| `output.path` | 额外输出文件。`sqlite` 不需要路径 | `data/<名称>.jsonl` |

`delay` 是每个 worker 自己的间隔。试跑时把 `workers` 调小，字段对了再加大。单次响应最大 8MB。页面会按声明转成 UTF-8。

## 代理

固定代理写成一行，支持 `http`、`https`、`socks5`：

```yaml
proxy: http://127.0.0.1:7890
```

要轮换 IP，改成接口或列表。`url`、`api`、`list` 只写一种。`every` 是同一条 IP 连续用多少次请求，接口和多条列表默认 20，写成 1 则每次请求都换。固定地址不轮换。

```yaml
proxy:
  api: http://127.0.0.1:5010/get
  every: 20
  scheme: http        # 接口只返回 ip:port 时补上协议。可选 http、https、socks5
  username: ""
  password: ""
```

接口可以返回 `ip:port`、完整地址、多行或逗号分隔，也可以是简单 JSON：`{"ip":"1.2.3.4","port":8080}`、`{"data":"1.2.3.4:8080"}`、`{"data":[{"ip":"1.2.3.4","port":8080}]}`。

自备列表：

```yaml
proxy:
  list:
    - http://1.2.3.4:8080
    - http://5.6.7.8:8080
  every: 10
```

列表里不要混用 http 和 socks5。网络错误、429、5xx 会丢掉当前 IP 再重试；代理池还会把 403、407 同样换掉。进度和结束摘要里显示当前代理，不含账号密码。切换也会写进 `gospider log`。

同名采集源再次 `source add` 或 `run -c` 会更新配置，已有记录不删除。
