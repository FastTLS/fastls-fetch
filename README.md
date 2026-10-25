# fastls-fetch

基于 HTTP 的 FastTLS Fetch 服务。它把 URL、请求头、Cookie、代理和 TLS 指纹配置转换为一次服务端 HTTP 请求，并以统一 JSON 结构返回结果。

> 当前状态：`v0.1.0` 正式版。正式产物只在 GitHub Releases 发布；PyPI、npm、容器仓库等渠道不在发布范围内。

## 能力

- `POST /fetch`：发起 HTTP/HTTPS 请求
- `GET /health`：服务健康检查
- 浏览器预设、JA3、JA4R 指纹配置
- 请求级代理、重定向、超时、Cookie、User-Agent 和请求 Trailer 控制
- 自动响应解压和 CORS
- SIGINT/SIGTERM 安全停止，给活动 HTTP 请求最多 5 秒完成，超时后强制关闭
- 基于 `fastls` + `ftransport` 的独立依赖链，不再依赖 `fhttp`

## 环境要求

- Go 1.26 或更高版本；CI 同时覆盖 Go 1.26.10 和 1.27.3
- 使用仓库自己的 `go.mod`，建议设置 `GOWORK=off`

## 从源码运行

```bash
git clone https://github.com/FastTLS/fastls-fetch.git
cd fastls-fetch
go mod download
go build -o fastls-fetch .
./fastls-fetch -addr :8800
```

Windows 使用 `go build -o fastls-fetch.exe .` 和 `fastls-fetch.exe -addr :8800`。

可用命令：

```bash
./fastls-fetch -help
./fastls-fetch -version
./fastls-fetch -addr 127.0.0.1:8800 -debug
```

## 快速请求

```bash
curl http://127.0.0.1:8800/health

curl -X POST http://127.0.0.1:8800/fetch \
  -H 'Content-Type: application/json' \
  -d '{
    "url": "https://example.com/",
    "method": "GET",
    "browser": "chrome"
  }'
```

成功响应包含 `ok`、`status`、`protocol`、`final_url`、`headers`、`trailers` 和 `body`；`trailers` 在响应体读取完成后返回并保留多值。参数或上游请求错误通过 HTTP 状态码以及 `error` 字段返回。完整字段、错误和多语言示例见 [Fetch API 文档](./fetch_server_example.md) 与 [客户端示例](./example/README.md)。

## 指纹选择

建议使用随指纹库滚动更新的稳定别名：`chrome`、`edge`、`firefox`、`safari`、`opera` 或 `chromium`。需要固定实验条件时，也可以使用当前支持的版本化选择器，例如 `chrome150` 至 `chrome160`、`edge150` 至 `edge160`、`firefox151` 至 `firefox158`，以及兼容保留的历史选择器。

请求中的优先级为：显式 `fingerprint`（JA3/JA4R）高于 `browser`，均未设置时使用默认浏览器配置。无法解析的指纹或浏览器选择器会返回错误，不会静默伪装为另一个版本。

请求体也支持 `http2_settings_string` 和 `parent_proxy`。代理按 `X-Mitm-Proxy` > `parent_proxy` > `proxy` 取值；显式 HTTP/2 设置覆盖浏览器预设；调用方取消 `/fetch` 请求时，下游请求也会同步取消。

## 依赖关系

```text
fastls-fetch
  └─ fastls
      ├─ ftransport       HTTP/1.1、HTTP/2、HTTP/3 传输
      ├─ fastls/profile   指纹模型
      └─ imitate          浏览器预设
```

CI 会拒绝本地 `replace` 指令和任何重新引入 `github.com/FastTLS/fhttp` 的依赖图。

## 测试与开发构建

本地最小验证：

```bash
go mod verify
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

GitHub Actions 覆盖 Windows、Linux、macOS 与两个 Go 版本，并执行依赖策略、单元/集成测试、竞态检测、覆盖率、静态检查、全包构建和 CLI 冒烟测试。`main` 分支生成保留 14 天的开发产物；`v*` 标签会用同一受审流程生成六个平台正式包、SHA-256 校验和和 Sigstore/GitHub 来源证明，并只发布到 GitHub Releases。

集成测试全部使用环回地址，覆盖请求体/请求头/Cookie、请求与响应 Trailer、最终协议/URL、压缩响应、禁用重定向、两级代理优先级、调用取消、CORS 预检和请求大小边界，不依赖公网服务。

测试套件还会现场构建并启动 `fastls-fetch`，通过真实 TCP 端口验证 `/health` 和 `/fetch`，避免只有进程内路由测试通过而 CLI 无法运行。

- [开发与贡献说明](./DEVELOPMENT.md)
- [开发产物及签发验证](./ARTIFACTS.md)

## 安全边界

该服务允许调用方指定目标地址和代理。JSON 请求体内置 4 MiB 上限，浏览器跨域请求会显式回显 `Origin` 后再允许凭据。不要把未加访问控制的实例暴露到不受信任网络；部署方仍应加入认证、目标地址策略、网络出口限制、更严格的资源限制和审计。

## 文档索引

- [Fetch API 文档](./fetch_server_example.md)
- [Python、Go、Node.js 客户端示例](./example/README.md)
- [开发流程](./DEVELOPMENT.md)
- [产物校验与来源证明](./ARTIFACTS.md)
