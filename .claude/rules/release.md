# Release Rules

## Release entrypoint
- Syrogo 的正式版本发布入口是推送 `v*` tag。
- 实际制品由 `.github/workflows/release.yml` 生成与发布。

## Pre-release verification
- 发布前必须先运行：
  - `make test`
  - `make lint`
  - `make build`
- 只有检查通过后，才允许继续打 tag。

## Workflow facts
- `release.yml` 的 job 顺序是：`verify -> package -> release`
- `release` job 仅在 `refs/tags/v*` 下执行。
- 产物包含多平台 tar.gz 与 checksum 文件。

## Collaboration
- 项目级发布 skill 位于 `.claude/skills/release/`。
- 当用户要求发布版本时，优先使用该 skill，并复用现有 workflow，而不是本地重写发布流程。
- 发布完成后，默认补一份适合 GitHub Release 的 release note 摘要，便于直接复用。
