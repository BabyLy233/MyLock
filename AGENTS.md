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
  空 `Unreleased`。
- 版本标题以版本号开头，后面可以空一格再写说明。`v0.2.0`、`[0.2.0]`、
  `[0.2.0](链接)` 都认；`## 0.2.0（2026-10-22）` 这种把日期直接粘在版本号
  后面的写法匹配不到。
- 一段更新说明遇到下一个 `## ` 标题就结束，所以段落内的子标题用 `###`，
  不要用 `##`。
- 一个版本一个段落，内容写用户能看懂的行为变化（新增 / 改动 / 修复），
  不写内部重构和文件清单。
- 不要删除旧版本段落：它们是发布记录；更新说明只取当前版本那一段。

## 发布

发布一个版本，就是从 `main` 上打一个 `v<version>` 标签，让
`.github/workflows/release.yml` 编译各平台、上传签名更新包，并把草稿 Release
发布出去。步骤：

1. 确认工作区干净，`make check` 通过。
2. 按上面的规则改 `CHANGELOG.md`：`Unreleased` 改成 `## <version>`，顶部补一个
   新的空 `Unreleased`。
3. 把 `mygo.json` 的 `version` 改成同一个 `<version>`，README「项目状态」里的
   当前版本也一起改。
4. 提交这次发布改动，例如 `chore: 发布 <version>`，推送到 `main`。
5. 打标签并推送：

   ```bash
   git tag -a v<version> -m "v<version>"
   git push origin v<version>
   ```

   标签会触发 release 流水线。也可以在 Actions 里手动运行 `release`，填写不带
   `v` 的版本号，效果相同。

发布时容易踩的点：

- 提交里的 `mygo.json` 版本必须已经等于标签版本。流水线虽然会按标签再写一次
  `mygo.json`，但紧接着要求工作区干净：Go 会把未提交的改动写进二进制，界面上
  的提交哈希会带 `-dirty`，所以版本不一致时构建会直接失败。一致时这一步什么
  也不改。不要指望 CI 替你把版本提交进去。
- 标签写 `v<version>`，`CHANGELOG.md` 的标题写 `<version>`；找不到对应段落时
  更新说明会退化成「See CHANGELOG.md」，`mygo build` 也可能报错，发布失败。
- 标签（或手动运行填的版本号）是版本的来源；`mygo.json` 里那份是构建时读取
  的副本，发版提交必须先和标签对齐。

## 待办

见 `TODO/`。
