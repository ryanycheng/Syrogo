# Git Rules

## Branch strategy
- 优先使用功能分支开发。
- 一个功能或一组强相关改动使用一个分支。
- 推荐分支命名：
  - `feat/<topic>`
  - `fix/<topic>`
  - `refactor/<topic>`
  - `chore/<topic>`
  - `docs/<topic>`
  - `test/<topic>`

## Commit granularity
- 优先小步提交。
- 一个 commit 只做一件事。
- 提交应便于回滚、排查和 review。
- 不把互不相关的改动混在同一个 commit 中。

## Commit message format
提交信息使用以下格式：

```text
<type>: 中文简短描述

- 详细说明 1
- 详细说明 2
- 详细说明 3

Author: <name> <email>
```

## Commit types
- `feat`：新功能
- `fix`：修复问题
- `refactor`：重构
- `chore`：工程配置或杂项调整
- `docs`：文档更新
- `test`：测试相关改动

## Author policy
- `Author` 优先从当前开发环境中查找。
- 如果当前开发环境无法可靠获得，则回退到 git 配置中的身份信息。
- 未能确认身份时，不要擅自猜测。

## Issue policy
- 当前阶段不强制关联 JIRA Issue 或 GitHub Issue。
- 当前项目以 0→1 探索和骨架建设为主，很多改动没有现成 issue 单。
- 在 issue 工作流尚未稳定前，优先通过分支名和提交说明写清楚改动背景。
- 等后续形成稳定的 issue 使用习惯后，再补充关联规则。

## Verification before commit
- 提交前优先保证本次改动的最小验证已完成。
- 常用验证命令：
  - `make fmt`
  - `make test`
  - `make run`
  - `golangci-lint run`
- 不要为了赶进度跳过必要验证。

## Collaboration guardrails
- 0→1 阶段优先保持提交历史清晰，不追求一次性大提交。
- 若改动涉及多个独立目标，应拆分为多个 commit。
- 若只是同一功能下强相关的小步演进，可以连续小步提交在同一功能分支中。
