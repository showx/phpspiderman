# phpspiderman

采集试验项目。PHP 版基于 Swoole 协程；现在日常使用 Go 版。

Go 版在 [`gospider/`](gospider/README.md)，一份配置就能跑，命令行里看进度和进程：

```bash
cd gospider
go run ./cmd/gospider run -c examples/api.yaml
```

只供技术研究，请勿用于非法用途。

## PHP 版

环境：PHP 7+、Swoole 4.4+。示例在 `demo/`。该实现依赖协程扩展和回调，进度要自己打印。新任务请用 Go 版。
