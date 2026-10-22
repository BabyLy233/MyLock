# MyLock

## 原项目

Tauri + React 写的原版在：

```
/home/soyorin/Dev/code/gitfork/deadlock-mod-manager
```

本仓库是它的 Go / Mygo 重写。行为以原项目为准；已经逐字节对齐的部分
（`.dmm.json` 清单格式、VPK 命名规则）改动时不要破坏兼容。

根目录只有入口 `main.go`，界面与应用编排在 `internal/app`，领域代码在
`internal/` 下的其它包。分层、线程模型和兼容性约束见 `ARCHITECTURE.md`。

## 更新记录

`CHANGELOG.md` 是应用自更新说明的唯一来源：`.github/workflows/release.yml`
从里面取 `## <version>` 段落，更新窗口直接把它当 Markdown 显示给用户。

- 顶部保留 `## Unreleased`，平时把改动先写在这里。
- 发版时把 `Unreleased` 改成 `## <version>`（不带 `v`），再在顶部补一个新的
  空 `Unreleased`。版本号必须和 `mygo.json` 的 `version` 一致；找不到对应
  段落时 `mygo build` 会报错，发布失败。
- 版本标题以版本号开头，后面可以空一格再写说明。`v0.2.0`、`[0.2.0]`、
  `[0.2.0](链接)` 都认；`## 0.2.0（2026-10-22）` 这种把日期直接粘在版本号
  后面的写法匹配不到。
- 一段更新说明遇到下一个 `## ` 标题就结束，所以段落内的子标题用 `###`，
  不要用 `##`。
- 一个版本一个段落，内容写用户能看懂的行为变化（新增 / 改动 / 修复），
  不写内部重构和文件清单。
- 不要删除旧版本段落：它们是发布记录；更新说明只取当前版本那一段。
- 发布标签是 `v<version>`，CI 会把标签里的版本注入 `mygo.json`；不要为了
  发版手改 `version` 并提交。

## 待办

见 `TODO/`。
