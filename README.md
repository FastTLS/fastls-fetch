# fastls-fetch

基于 HTTP 的 FastTLS Fetch 服务。它把 URL、请求头、Cookie、代理和 TLS 指纹配置转换为一次服务端 HTTP 请求，并以统一 JSON 结构返回结果。

> 当前状态：开发阶段。仓库只生成带来源证明、保留 14 天的开发构建，不创建版本标签、GitHub Release 或正式发布包。

## 能力

- `POST /fetch`：发起 HTTP/HTTPS 请求
- `GET /health`：服务健康检查
- 浏览器预设、JA3、JA4R 指纹配置
- 请求级代理、重定向、超时、Cookie 和 User-Agent 控制
- 自动响应解压和 CORS
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

成功响应包含 `ok`、`status`、`headers` 和 `body`；参数或上游请求错误通过 HTTP 状态码以及 `error` 字段返回。完整字段、错误和多语言示例见 [Fetch API 文档](./fetch_server_example.md) 与 [客户端示例](./example/README.md)。

## 指纹选择

建议使用随指纹库滚动更新的稳定别名：`chrome`、`edge`、`firefox`、`safari`、`opera` 或 `chromium`。需要固定实验条件时，也可以使用当前支持的版本化选择器，例如 `chrome150` 至 `chrome160`、`edge150` 至 `edge160`、`firefox151` 至 `firefox158`，以及兼容保留的历史选择器。

请求中的优先级为：显式 `fingerprint`（JA3/JA4R）高于 `browser`，均未设置时使用默认浏览器配置。无法解析的指纹或浏览器选择器会返回错误，不会静默伪装为另一个版本。

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

GitHub Actions 覆盖 Windows、Linux、macOS 与两个 Go 版本，并执行依赖策略、单元/集成测试、竞态检测、覆盖率、静态检查、全包构建和 CLI 冒烟测试。`main` 分支还会构建六个开发目标，生成 SHA-256 校验和和 Sigstore/GitHub 来源证明，并在上传前再次验证证明。

- [开发与贡献说明](./DEVELOPMENT.md)
- [开发产物及签发验证](./ARTIFACTS.md)

## 安全边界

该服务允许调用方指定目标地址和代理。不要把未加访问控制的实例暴露到不受信任网络；部署方应自行加入认证、目标地址策略、网络出口限制、请求大小限制和审计。开发构建不承诺稳定 API 或生产支持。

## 文档索引

- [Fetch API 文档](./fetch_server_example.md)
- [Python、Go、Node.js 客户端示例](./example/README.md)
- [开发流程](./DEVELOPMENT.md)
- [产物校验与来源证明](./ARTIFACTS.md)
