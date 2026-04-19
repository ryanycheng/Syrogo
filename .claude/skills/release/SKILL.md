---
name: release
description: 在 Syrogo 仓库中执行版本发布。用于发布前校验、规范化版本号、创建并推送 v* tag，并提示检查 GitHub Actions release 流程。
---

# Release

这个 skill 用于在 Syrogo 仓库中执行一次标准版本发布。

## 何时使用

- 用户要求“发布版本”“创建 release”“打 tag 发布”
- 用户想按当前仓库既有流程发布 `vX.Y.Z`
- 需要先做发布前检查，再推送 tag 触发 GitHub Release

## 仓库事实

- GitHub Actions 发布入口：`.github/workflows/release.yml`
- 触发条件：push tag `v*`
- job 顺序：`verify -> package -> release`
- 本地检查命令：
  - `make test`
  - `make lint`
  - `make build`

## 输入约定

接受以下版本参数：

- `v0.1.0`
- `0.1.0`

实现时统一规范化为 `vX.Y.Z`。

## 执行步骤

1. 确认当前目录是 Syrogo git 仓库。
2. 检查工作区是否干净；若不干净，先停止并告知用户。
3. 规范化并校验版本号，必须与 `v*` tag 规则兼容。
4. 运行发布前检查：
   - `make test`
   - `make lint`
   - `make build`
5. 在创建远端可见变更前，明确提醒：接下来会创建并推送 tag，这将触发 GitHub Actions release workflow。
6. 创建 annotated tag。
7. 推送该 tag 到远端。
8. 返回后续检查项：
   - 查看 GitHub Actions 中 `verify`、`package`、`release` 是否成功
   - 查看 GitHub Release 是否包含 tar.gz 制品与 checksum

## 边界

- 不自动生成 changelog 或 release notes
- 不自动修改代码中的版本号
- 不重写已有打包逻辑；复用 `.github/workflows/release.yml`
- 如果用户未明确要求实际发布，只做检查和说明，不推 tag

## 结果要求

完成后应明确输出：

- 实际发布的 tag
- 是否已成功推送
- 需要检查的 workflow / release 项目
