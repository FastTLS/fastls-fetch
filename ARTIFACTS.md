# 开发产物与来源证明

## 范围

`Development artifacts` 工作流只为测试和联调提供临时构建。它在 `main` 更新或手动触发时运行，不创建 tag、GitHub Release 或正式版本。Actions 产物保留 14 天。

每次运行生成 Linux、Windows、macOS 的 amd64/arm64 包。归档名包含 `dev-<commit前12位>`，包内包含可执行文件和 `BUILD_INFO.txt`。

## 下载

从仓库的 Actions 页面打开对应运行并下载 `fastls-fetch-development-<完整提交SHA>`，或使用 GitHub CLI：

```bash
gh run list --repo FastTLS/fastls-fetch --workflow development-artifacts.yml
gh run download RUN_ID \
  --repo FastTLS/fastls-fetch \
  --name fastls-fetch-development-COMMIT_SHA \
  --dir fastls-fetch-artifacts
```

下载目录包含平台归档、`SHA256SUMS` 和 `provenance.sigstore.json`。

## 校验完整性

在下载目录执行：

```bash
sha256sum --check SHA256SUMS
```

macOS 可逐项使用 `shasum -a 256 文件名` 与 `SHA256SUMS` 对照；PowerShell 可用 `Get-FileHash 文件名 -Algorithm SHA256`。

## 在线验证签发来源

安装 GitHub CLI 后，对实际要运行的归档执行：

```bash
gh attestation verify fastls-fetch-dev-COMMIT-linux-amd64.tar.gz \
  --repo FastTLS/fastls-fetch \
  --signer-workflow FastTLS/fastls-fetch/.github/workflows/development-artifacts.yml
```

验证成功表示归档摘要与 GitHub 保存的签名证明一致，并且签发者是本仓库指定工作流。它不代表该构建已成为正式版本。

## 使用随包证明验证

随包的 `provenance.sigstore.json` 可用于固定验证内容：

```bash
gh attestation verify fastls-fetch-dev-COMMIT-linux-amd64.tar.gz \
  --repo FastTLS/fastls-fetch \
  --bundle provenance.sigstore.json \
  --signer-workflow FastTLS/fastls-fetch/.github/workflows/development-artifacts.yml
```

完全离线验证还需要预先取得并可信传入 Sigstore trusted root；可在联网环境执行 `gh attestation trusted-root > trusted_root.jsonl`，离线时额外传入 `--custom-trusted-root trusted_root.jsonl`。

## 签发链路

1. 固定 Go 工具链和第三方 Action 提交。
2. 校验 module、测试和静态检查。
3. 使用 `CGO_ENABLED=0`、`-trimpath` 和嵌入的提交信息交叉构建。
4. 对所有归档计算 SHA-256。
5. 通过 GitHub OIDC 与 Sigstore 为归档摘要生成 SLSA provenance。
6. 使用限定仓库和工作流身份的命令验证证明，成功后才上传开发产物。
