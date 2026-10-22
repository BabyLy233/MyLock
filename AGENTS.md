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

## 待办

见 `TODO/`。
