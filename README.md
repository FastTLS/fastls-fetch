# fastls-fetch

[English documentation](https://fasttls.github.io/en/ecosystem/fastls-fetch)

基于 HTTP 的 FastTLS Fetch 服务。它把 URL、请求头、Cookie、代理和 TLS 指纹配置转换为一次服务端 HTTP 请求，并以统一 JSON 结构返回结果。

> 当前功能版本：`v0.2.0`；正式二进制以 [GitHub Releases](https://github.com/FastTLS/fastls-fetch/releases) 为准。正式产物只在 GitHub Releases 发布；PyPI、npm、容器仓库等渠道不在发布范围内。

新增 [显式出站 H3](H3.md)：三入口统一 `http_version=h3`、证书校验、可信启动 CA 与地址策略组合；缺省 H1/H2 保持。gRPC 请求新增字段 16，其余字段不变。

本版本提供完整多值响应头、原始正文 Base64、错误分类和可选响应字节/并发上限，见[响应契约](RESPONSE_CONTRACT.md)。

v0.1.2 包含 [接入资源限额](INGRESS.md)：TCP 总名额、读取正文前的接收名额、头部预算及读取/空闲时限，均为可信启动配置，默认兼容现有行为。

[使用文档](https://fasttls.github.io/ecosystem/) · [下载发布包](https://github.com/FastTLS/fastls-fetch/releases)

下载对应系统/架构的 Release 产物后校验 SHA-256 和来源证明，解压运行；普通用户无需安装 Go。只有从源码构建才需要下述 Go 工具链。

## 能力

- `POST /fetch`：发起 HTTP/HTTPS 请求
- `GET /health`：服务健康检查
- 浏览器预设、JA3、JA4R 指纹配置
- 请求级代理、重定向、超时、Cookie、User-Agent 和请求 Trailer 控制
- 自动响应解压和 CORS
- SIGINT/SIGTERM 安全停止，给活动 HTTP 请求最多 5 秒完成，超时后强制关闭
- 基于 `fastls` + `ftransport` 的独立依赖链，不再依赖 `fhttp`

## 环境要求

v0.1.2 的独立发行二进制支持可信 `-address-policy` 启动配置，并拒绝未指定目标地址，见[地址策略与升级](ADDRESS_POLICY.md)。策略默认关闭，旧二进制需显式升级。

- Go 1.26 或更高版本；CI 同时覆盖 Go 1.26.10 和 1.27.3
- 使用仓库自己的 `go.mod`，建议设置 `GOWORK=off`

默认 `-addr :8800` 监听所有可用接口，并非仅环回；下面示例显式使用 127.0.0.1。开放访问前需配置访问控制。

## 从源码运行

```bash
git clone https://github.com/FastTLS/fastls-fetch.git
cd fastls-fetch
go mod download
go build -o fastls-fetch .
./fastls-fetch -addr 127.0.0.1:8800
```

Windows 使用 `go build -o fastls-fetch.exe .` 和 `fastls-fetch.exe -addr 127.0.0.1:8800`。

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
      ├─ ftransport       HTTP/1.1、HTTP/2 传输（H3 为核心独立路径）
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

GitHub Actions 覆盖 Windows、Linux、macOS 与两个 Go 版本，并执行依赖策略、单元/集成测试、竞态检测、覆盖率、静态检查、全包构建和 CLI 冒烟测试。手动工作流生成保留 14 天的开发产物；`v*` 标签会用同一受审流程生成六个平台正式包、SHA-256 校验和和 Sigstore/GitHub 来源证明，并只发布到 GitHub Releases。

集成测试全部使用环回地址，覆盖请求体/请求头/Cookie、请求与响应 Trailer、最终协议/URL、压缩响应、禁用重定向、两级代理优先级、调用取消、CORS 预检和请求大小边界，不依赖公网服务。

测试套件还会现场构建并启动 `fastls-fetch`，通过真实 TCP 端口验证 `/health` 和 `/fetch`，避免只有进程内路由测试通过而 CLI 无法运行。

- [开发与贡献说明](./DEVELOPMENT.md)
- [开发产物及签发验证](./ARTIFACTS.md)

## 安全边界

该服务允许调用方指定目标地址和代理。JSON 请求体默认 4 MiB 上限；v0.1.2 可用`-max-request-bytes`设置1字节至64MiB（0选择默认值），完整正文包含尾部数据均计入。浏览器跨域请求会显式回显 `Origin` 后再允许凭据。不要把未加访问控制的实例暴露到不受信任网络；部署方仍应加入认证、目标地址策略、网络出口限制、更严格的资源限制和审计。

## 文档索引

- [Fetch API 文档](./fetch_server_example.md)
- [Python、Go、Node.js 客户端示例](./example/README.md)
- [开发流程](./DEVELOPMENT.md)
- [产物校验与来源证明](./ARTIFACTS.md)


## 不支持与部署责任

这是受控网络内的 HTTP 请求服务，不是完整浏览器、通用 VPN 或任意协议转发器。不能从核心的 HTTP/3 路径推断所有服务入口均支持 H3 指纹、代理组合或流式 RPC。默认示例不能替代认证、出口控制、租户隔离和流量限制。浏览器别名是服务适配层选择器，与 Go Provider 的规范 ID 不同；实际支持范围以该发布版本为准。

## 详细文档与问题反馈

- [使用文档](https://fasttls.github.io/ecosystem/)：安装、功能、参数与排错。
- [版本与发布说明](https://github.com/FastTLS/fastls-fetch/releases)：正式版本、开发版本和产物边界。
- [问题反馈](https://github.com/FastTLS/fastls-fetch/issues)：请提供版本、系统架构、脱敏后的最小示例与错误；不要提交凭据、Cookie、私钥或个人数据。

## 开源协议与第三方声明

FastTLS 自有代码采用 [Apache-2.0](LICENSE)，见 [NOTICE](NOTICE)。依赖及发布包内的第三方声明保留各自授权。

## 使用边界与免责声明

仅在拥有权限的系统、数据和网络上使用，并遵守适用法律、服务条款及隐私要求。软件按许可证约定以“现状”提供，不承诺可用性、特定用途适用性、第三方服务放行、匿名性或与真实浏览器完全一致。浏览器名称和版本用于描述配置，不表示获得相应厂商认证或背书。部署方负责访问控制、密钥和个人信息保护、请求限额及数据备份；担保排除与责任限制以随附许可证及适用法律为准。本说明不改变许可证授予的权利或新增许可限制。

## Optional audit (released since v0.1.1)

[Optional audit events and limits](AUDIT.md): disabled by default, with fixed redacted fields and bounded drain. Available in service v0.1.1 and v0.1.2; auditing remains opt-in.

发行门禁接受同一完整提交的成功候选，或独立重新核验过的可信 main 祖先证明。复用要求源码、模块、生成输入、原生命令、工具链及平台配置一致，全部必需任务成功；未知差异、缺失、失败、取消和任意跳过均阻止签发。RPC 的跨仓契约单独检查固定引用、协议测试与执行配置，不随普通 Go 证明自动豁免。归档继续按实际生产提交构建、记录 vcs.revision 并签名，不将旧字节改标为新提交。main 不自动重复生成开发归档，正式标签每个平台只构建一次。
