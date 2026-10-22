<p align="center">
  <img src="resources/icon.png" width="120" alt="MyLock">
</p>

<h1 align="center">MyLock</h1>

<p align="center">Deadlock 的模组管理器，用 Go + Mygo 写的原生桌面应用。</p>

MyLock 从 [GameBanana](https://gamebanana.com/games/20948) 浏览和安装 Deadlock 模组，把 VPK 放进游戏目录、写好 `gameinfo.gi`，再交给 Steam 启动。界面由 Mygo 直接绘制，不加载网页，也不需要 WebView。

与 Tauri + React 的[原版](https://github.com/deadlock-mod-manager/deadlock-mod-manager)共享 `.dmm.json` 清单格式和 VPK 命名规则，两边可以识别彼此安装的模组。

代码怎么分层、各部分如何协作，见 [ARCHITECTURE.md](ARCHITECTURE.md)。

## 功能

**模组商店**

- 浏览模组、音效和制作中的作品，按最近更新、最多点赞或最多下载排序
- 搜索；按根分类和子分类（Skins 下是英雄）筛选；可以隐藏 NSFW 条目
- 详情页显示作者说明、截图和文件列表，截图支持大图查看、缩放和前后翻页

**安装与启停**

- 下载带进度显示，支持断点续传；API 给了大小和 MD5 时会校验
- 支持 `.zip`、`.rar`、`.7z`，在压缩包里找出所有 VPK，装进当前 profile 并自动启用
- 一个模组有多个下载文件时（变体、附加包）可以勾选多个一起安装；重名的 VPK 按压缩包分别命名，互不覆盖
- 已安装模组按游戏加载顺序列出，每项都有开关；缺文件时会提示
- 启停靠移动和重命名 VPK 完成，不复制文件，切换很快
- 游戏运行期间禁止装卸模组，避免游戏读到写了一半的文件

**更新检查**

- 打开「模组管理」时自动检查，也可以随时点「检查更新」
- 按安装时记下的下载文件判断有没有新版本：作者只改了说明不会误报
- 更新会重新下载安装时选中的所有文件，多文件模组不会只剩其中一个
- 卡片上标出新版本的日期，点「更新」下载并替换原文件，保留启用状态
- 更新失败时原版本会被放回去，不会留下一个装了一半的模组

**启动游戏**

- 「启动模组版游戏」写入当前 profile 的搜索路径后交给 Steam
- 「启动原版游戏」把 `gameinfo.gi` 改回游戏自带的样子，退出后自动恢复模组
- 支持 Steam 自动检测（会遍历所有库），也可以手动指定包含 `game/citadel` 的目录
- Linux / macOS 上通过 Steam 的 Proton / Wine 启动 Windows 版游戏

**其它**

- 图片按显示尺寸解码，原始字节缓存在磁盘；128 MiB 上限，LRU 淘汰，设置页可查看和清除
- 设置页可切换浅色 / 深色 / 跟随系统
- 只写游戏的 `addons` 目录和 `gameinfo.gi`，不改游戏本体

## 构建

需要 Go 1.27 或更新版本。Mygo 的版本固定在 `go.mod` 里，用 `go tool mygo` 调用，不必另外安装。

```bash
git clone https://github.com/BabyLy233/mylock.git
cd mylock

make build   # 只编当前平台
make         # 编译所有平台的安装包
make run     # 直接运行，不打包
make check   # go vet + mygo vet + 全部测试
```

产物在 `build/<系统>-<架构>/` 下：

| 平台 | 产物 |
| --- | --- |
| Linux | `mylock`、`.deb`、`.tar.gz` |
| Windows | `mylock.exe`、安装包 |
| macOS | `.app`、`.dmg`（签名和 `.dmg` 需要 macOS） |

## 使用

1. 打开「设置」，等它自动找到 Deadlock；找不到就手动选到包含 `game/citadel` 的那一层。
2. 如果游戏更新覆盖了 `gameinfo.gi`，在「设置」里点一次「应用模组路径」。
3. 到「模组商店」浏览或搜索，打开一个模组，选择文件后点「安装」。
4. 回到「模组管理」开关模组，用「启动模组版游戏」进入游戏。

## 数据位置

配置和图片缓存都放在各平台的标准目录里：

| 用途 | Linux | Windows | macOS |
| --- | --- | --- | --- |
| 设置 | `~/.config/mylock/config.json` | `%AppData%\mylock\config.json` | `~/Library/Application Support/mylock/config.json` |
| 图片缓存 | `~/.cache/mylock/images` | `%LocalAppData%\mylock\images` | `~/Library/Caches/mylock/images` |

从改名前版本升级时，第一次启动会把旧的 `deadlock-mod-manager/config.json` 复制到新位置，旧文件保留不动。旧的图片缓存不会迁移，可以直接删除。

## 兼容性

- `.dmm.json` 按原版逐字节读写，清单版本为 3
- VPK 命名规则与原版一致，包括 `pakNN_dir.vpk` 的分片和前缀规则
- `gameinfo.gi` 里使用的开始 / 结束标记与原版相同，已有的游戏配置能被识别和替换

## 项目状态

还在早期开发中，当前版本 `0.1`。

已实现商店、安装、启停、更新检查和启动。尚未实现：

- 一次更新多个模组
- 跳过某个模组的更新
- 多余启动参数和其它启动方式

更多计划见 [`TODO/`](TODO/)。

## 致谢

- 模组数据来自 [GameBanana](https://gamebanana.com)
- 行为与文件格式对齐 Tauri 原版：[deadlock-mod-manager/deadlock-mod-manager](https://github.com/deadlock-mod-manager/deadlock-mod-manager)
- 界面框架：[Mygo](https://github.com/egoist/mygo)

MyLock 是非官方工具，与 Valve 无关。Deadlock 是 Valve Corporation 的商标。
