# 接入资源边界（v0.1.2）

本页选项已包含在正式 v0.1.2；v0.1.0 是不包含这些选项的历史版本。它们属于可信启动配置，不从请求头或 JSON DTO 读取；不要求新核心地址策略能力，`GOWORK=off` 构建也可使用。默认新增名额均为 0，保持原有不限制行为；非法值在监听前拒绝。

## 连接、接收与执行名额

- `-max-connections N`：0..65536。限制本实例已经接受并登记的 TCP 连接，包含尚未完成头部的连接、慢正文、keepalive、普通请求和健康检查。满额时在 Accept 后立即关闭新连接，不排队、不解析 HTTP，也不承诺 HTTP 503。内核 backlog 和正在拒绝的临时套接字不属于该计数。
- `-max-admitted-requests N`：0..65536。完成 HTTP 头解析后、读取完整 JSON 正文之前申请名额，直到 HTTP handler 返回；所有路径、OPTIONS 和健康检查都计入。满额返回 HTTP 503，JSON 含 `ok:false`、`error_code:overloaded`，关闭本次连接，停止自动排空未读正文，避免拒绝响应等待慢调用方。
- 已有 `-max-inflight` 仍是 fetch 路由的执行名额，位置与原行为保留。新名额不替代它，也不能保护已经发生的 HTTP 头解析；头部之前的边界由 TCP 名额、读头时限和头部预算共同提供。

连接名额在底层关闭时幂等归还，HTTP 名额在处理退出时归还。超限、无效 JSON、断连和取消均不能永久占用名额。健康检查本身也需要连接和请求名额，满额时不能把其 503 直接解释为进程故障。

## 头部与读取时限

- `-max-header-bytes N`：0..16777216。0 保留 net/http 默认；非零传入 `http.Server.MaxHeaderBytes`，遵循标准库的解析预算和缓冲余量，不宣称是原始线上字节的精确 N/N+1 截止。超大头部在业务处理前由协议服务器拒绝，通常为 431。
- `-read-header-timeout D`：默认 10s；0 选择原有 10s；允许 0..24h。
- `-read-timeout D`：默认 0，保持不添加正文读取截止时间；非零限制单请求头部及正文读取总时间，允许至 24h。不是只要有字节进来就续期的空闲计时器，也不改变原有出站请求 timeout。
- `-idle-timeout D`：默认 0，采用 net/http 的回退规则，可能回退到已配置的 read-timeout；非零限制等待下一条 keepalive 请求的时间，允许至 24h。

读取超时依所在解析阶段可能关闭连接或返回解析错误，不统一伪装为 HTTP 408 或成功结果。`-max-request-bytes` 原有完整 JSON 预算仍生效，包含尾随字节；这些启动级限制不能由 DTO 或 X-Mitm-* 头清除。

## 停机与范围

取消或 SIGINT/SIGTERM 停止接收新连接，仍给活动请求原有的 5 秒宽限期。到期后关闭所有登记连接，取消相关处理并归还名额；保留 deadline 错误和非零退出状态，不报告成优雅完成。

本批不添加入站 HTTP/2、速率限制、全实例流量预算或总 RSS 保证。上游 HTTP/2 支持与入站 HTTP/1 接入边界分别验证。

例如，开发二进制可显式配置：

```sh
fastls-fetch -addr 127.0.0.1:8800 -max-connections 128 -max-admitted-requests 16 -max-inflight 8 -max-header-bytes 65536 -read-header-timeout 5s -read-timeout 30s -idle-timeout 30s
```

## 验证

`go test ./...` 和竞态检查覆盖限额、取消、并发重复关闭、慢读、空闲连接和停机。RPC 仓库的 `workspace_e2e` 使用真实 Fetch/JSON-RPC/gRPC CLI 验证启动拒绝、接入名额、正文读取前拒绝、读取时限、协议错误和失败后恢复；协调工作流以固定三仓 SHA 执行三平台、双 Go 的三轮检查。原发布依赖与显式新核心策略门禁分别保留。
