# 贡献指南 | Contribution Guidelines

感谢您为项目做出贡献，请遵循以下规范。 | Thank you for contributing to the project. Please follow these guidelines.

## PR 范围 | PR Scope

- 每个 PR 只允许包含一个变更主题（一个新增功能或一个BUG修复）。 | Each PR may contain only one change topic (one new feature or one bug fix).
- 若存在多个变更主题，请拆分为多个独立 PR 提交。 | If there are multiple change topics, please split them into separate PRs.

## AI 使用规范 | AI Usage Guidelines

- 允许使用 AI 辅助。 | AI assistance is permitted.
- 提交者对最终内容负责，提交前必须完成人工审查。 | Contributors are responsible for the final content and must complete a manual review before submission.

## 提交前检查 | Pre-submission Checklist

- [ ] 本次 PR 仅包含一个变更主题 | This PR contains only one change topic
- [ ] 契约/行为修复包含对应测试；纯文档或纯文案 PR 才可以不带测试文件 | Contract and behavior fixes include tests; docs-only or copy-only PRs may omit test files
- [ ] AI 参与的内容已完成人工审查 | AI-assisted content has been manually reviewed

## 开发与测试 | Development & Testing

- 一个 PR 只做一类契约或一个页面，不要混分层重构。 | One PR, one contract or one page; do not mix layering refactors.
- 本仓库的 AI 改动默认推 GitHub Actions 验收（`web-next-ci`、`build`），不强制贡献者禁止本地测试。 | AI edits in this repo default to GitHub Actions (`web-next-ci`, `build`) as the gate; contributors may still run tests locally.
- 行级虚拟列表 DOM 断言放 Playwright，不要写进 jsdom/vitest。 | Assert virtualized row DOM in Playwright, not jsdom/vitest.
