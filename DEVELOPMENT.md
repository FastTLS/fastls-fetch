# 开发说明

## 开发基线

仓库是独立 Go module。开发和 CI 均应使用 `GOWORK=off`，不得依赖父目录工作区、未提交的本地模块或 `replace` 指令。

```bash
go version
go mod download
go mod verify
go test ./...
go vet ./...
go build ./...
```

最低基线为 Go 1.26；CI 的兼容矩阵为 Go 1.26.10/1.27.3 × Ubuntu 24.04/Windows 2022/macOS 15。

## 提交前检查

```bash
go mod tidy
git diff --exit-code -- go.mod go.sum
go test -race ./...
go test -covermode=atomic -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

涉及网络行为的修改应使用本地 `httptest`/TLS 测试服务器，避免把公网服务稳定性当成测试前提。新增指纹选择器时，应同时覆盖规范选择器、兼容别名和无效输入。

服务生命周期变更必须验证 context 取消、监听器关闭以及活动请求在超时窗口内完成后才退出。测试只监听环回地址。

## CI 门禁

`.github/workflows/ci.yml` 包含三组门禁：

1. 依赖策略：模块校验、`go mod tidy` 零差异、禁止 `replace`、禁止 `fhttp`。
2. 平台矩阵：所有平台和 Go 版本执行测试、`go vet` 和全包构建。
3. 质量检查：Linux 竞态测试、覆盖率输出、二进制构建及 `-version` 冒烟测试。

所有第三方 Actions 都固定到完整提交 SHA。Pull Request 只获得只读仓库权限。

## 开发产物

合入 `main` 或手动运行 `Development artifacts` 后，工作流会再次验证源码，再交叉构建、计算校验和、签发来源证明并自验证。它不创建标签或 Release。格式、下载及验证步骤见 [ARTIFACTS.md](./ARTIFACTS.md)。

## 变更约定

- 行为变化要同步更新 README、API 文档和示例。
- 保持错误可诊断；不要把无效指纹静默降级为其他指纹。
- 新依赖必须能在独立 module 下解析，且不能重新引入 `fhttp`。
- 提交生成文件前说明其生成命令，并确认生成结果可复现。
