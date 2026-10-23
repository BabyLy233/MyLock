<p align="center">
  <img src="resources/icon.png" width="120" alt="MyLock">
</p>

<h1 align="center">MyLock</h1>

<p align="center">Deadlock 的模组管理器，用 Go + Mygo 写的原生桌面应用。</p>

MyLock 从 [GameBanana](https://gamebanana.com/games/20948) 浏览和安装 Deadlock 模组，把 VPK 放进游戏目录、写好 `gameinfo.gi`，再交给 Steam 启动。界面由 Mygo 直接绘制，不加载网页，也不需要 WebView。

与 Tauri + React 的[原版](https://github.com/deadlock-mod-manager/deadlock-mod-manager)共享 `.dmm.json` 清单格式和 VPK 命名规则，两边可以识别彼此安装的模组。

代码怎么分层、各部分如何协作，见 [ARCHITECTURE.md](ARCHITECTURE.md)。

## 安装

从 [Releases](https://github.com/BabyLy233/MyLock/releases/latest) 下载对应平台的包。
没写架构的安装包按机器选择：普通 Intel / AMD 电脑用 `amd64`，ARM 设备用
`arm64`。

### Windows

**安装版**：下载 `MyLock Setup <版本> amd64.exe`（ARM 设备用 `arm64`），运行后
按提示安装。默认装在当前用户目录，不需要管理员权限。

**便携版**：下载 `MyLock.exe`，放进任意可写目录直接运行。放在 `Program Files`
这类没有写权限的位置时不能自动更新，请手动下载新版本。

目前没有代码签名，首次运行可能出现 SmartScreen 提示，点「更多信息」→
「仍要运行」即可。

### Linux

推荐用安装脚本，装到当前用户目录，不需要 root：

```bash
curl -fsSL https://github.com/BabyLy233/MyLock/releases/latest/download/install.sh | sh
```

也可以手动下载：

- `mylock-<版本>-linux-amd64.tar.gz` / `linux-arm64.tar.gz`：解压后直接运行
  `./mylock`。想装进应用菜单，就把同一 Release 里的 `install.sh` 放到压缩包
  旁边，执行 `sh install.sh mylock-<版本>-linux-amd64.tar.gz`（不传参数则
  下载最新版），它会装到 `~/.local/mylock.app`。
- `mylock_<版本>_amd64.deb` / `_arm64.deb`：Debian / Ubuntu 上用
  `sudo apt install ./mylock_<版本>_amd64.deb` 安装。包管理器安装的版本不能
  自更新，随系统升级。

卸载：`sh install.sh --uninstall`（脚本安装）；`.deb` 用
`sudo apt remove mylock`。

## 使用

1. 打开「设置」，等它自动找到 Deadlock；找不到就手动选到包含 `game/citadel` 的那一层。
2. 如果游戏更新覆盖了 `gameinfo.gi`，在「设置」里点一次「应用模组路径」。
3. 到「模组商店」浏览或搜索，打开一个模组，选择文件后点「安装」。
4. 回到「模组管理」开关模组，用「启动模组版游戏」进入游戏。

## 功能

**模组商店**

- 浏览模组、音效和制作中的作品，按最近更新、最多点赞或最多下载排序
- 搜索；按根分类和子分类（Skins 下是英雄）筛选；可以隐藏 NSFW 条目
- 详情页显示作者说明、截图和文件列表，截图支持大图查看、缩放和前后翻页
- 详情页可以复制分享链接；把链接粘贴到搜索框就能直接打开对应模组，列表里
  没有它也可以，方便把模组发给其他使用 MyLock 的人

**安装与启停**

- 下载显示速度和进度，侧边栏底部始终能看到正在进行的下载；「查看」打开独立的下载窗口，按文件显示进度，可以暂停、继续和取消
- 支持同时下载多个模组和更新，写入配置档的最后一步会排队，不会互相覆盖
- 支持断点续传：暂停后再继续，只从断开的地方接着下；API 给了大小和 MD5 时会校验
- 支持 `.zip`、`.rar`、`.7z`，在压缩包里找出所有 VPK，装进当前 profile 并自动启用
- 一个模组有多个下载文件时（变体、附加包）可以勾选多个一起安装；重名的 VPK 按压缩包分别命名，互不覆盖
- 已安装模组按游戏加载顺序列出，每项都有开关；缺文件时会提示
- 一个模组带多个 VPK 时，可以点「文件选择」单独勾选要启用的文件；未勾选的文件留在配置档里，随时可以再勾上
- 启停靠移动和重命名 VPK 完成，不复制文件，切换很快
- 游戏运行期间禁止装卸模组，避免游戏读到写了一半的文件

**角色皮肤**

- 「角色皮肤」页按英雄分组列出已安装的皮肤，有模组的英雄排在前面，可以搜索
- 一个角色默认只穿一套皮肤：穿上新的会先停用旧的（文件保留），再点当前这套
  就是脱下，回到默认外观
- 从商店安装皮肤时，如果该角色已经有皮肤，会问「交换 / 两者都留 / 取消」
- 设置里可以允许同一角色同时启用多套皮肤、关掉安装时的冲突提醒；已经同时
  启用的多套只会在角色列表上标出，不会自动改动
- 角色按 Skins 分类识别，也可以从模组标题的别名猜，或者在「模组管理」里
  手动指定 / 恢复自动

**更新检查**

- 打开「模组管理」时自动检查，也可以随时点「检查更新」
- 按安装时记下的下载文件判断有没有新版本：作者只改了说明不会误报
- 更新会重新下载安装时选中的所有文件，多文件模组不会只剩其中一个
- 卡片上标出新版本的日期，点「更新」下载并替换原文件，保留启用状态
- 更新失败时原版本会被放回去，不会留下一个装了一半的模组

**应用更新**

- 启动后自动检查（每天一次），也可以在设置页手动检查
- 更新窗口显示版本、日期和更新说明，可以安装、稍后提醒或跳过这个版本
- 下载有进度，更新包带 Ed25519 签名，校验通过后原地替换并重启
- 开发构建和包管理器安装的版本（`.deb`、AppImage、只读的 `/Applications`）
  会说明为什么不能自更新，不显示会失败的按钮

**启动游戏**

- 「启动模组版游戏」写入当前 profile 的搜索路径后交给 Steam
- 「启动原版游戏」把 `gameinfo.gi` 改回游戏自带的样子，退出后自动恢复模组
- 支持 Steam 自动检测（会遍历所有库），也可以手动指定包含 `game/citadel` 的目录
- Linux / macOS 上通过 Steam 的 Proton / Wine 启动 Windows 版游戏

**其它**

- 图片按显示尺寸解码，原始字节缓存在磁盘；128 MiB 上限，LRU 淘汰，设置页可查看和清除
- 设置页可切换浅色 / 深色 / 跟随系统
- 下载可走 GameBanana 的文件服务器：设置页可选默认、自动或指定一台，支持刷新列表和测速
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

## 发布

应用更新走 GitHub Releases：标签为 `v<版本>`，每个平台上传签名过的更新包和
`update-<target>.json`。`.github/workflows/release.yml` 会在推标签或手动运行时
编译全部平台，先建草稿，全部上传成功后再发布。

一次性准备：

1. 生成签名密钥，只需一次：

   ```bash
   go tool mygo keygen
   ```

   它写出 `mygo-update.key`（私钥）和 `mygo-update.pub`（公钥），默认在系统的
   配置目录下（Linux 是 `~/.config/mygo/update-keys/`）。把公钥内容填进
   `mygo.json`，私钥只放进 GitHub Secret `MYGO_UPDATER_PRIVATE_KEY` 和
   密码管理器，**不要提交**。
   私钥丢失后已安装的用户无法再自动更新；私钥泄露等同于任何人都能推送
   任意代码。

2. 在 `mygo.json` 增加更新配置：

   ```json
   "updates": {
     "publicKey": "<mygo-update.pub 的内容>",
     "github": "BabyLy233/MyLock",
     "changelog": "CHANGELOG.md"
   }
   ```

3. 仓库必须是公开的：已安装的应用直接读 latest release 里的静态文件，
   不经过 GitHub API，也不带登录凭据。

4. 发版前在 `CHANGELOG.md` 写 `## <版本>`，段落标题要和 `mygo.json` 的
   `version` 完全一致。然后推 `v<版本>` 标签，或用 Actions 里的
   `release` 手动指定版本运行。

macOS 正式分发还需要 Apple Developer ID 签名和公证，Windows 建议做代码
签名，否则用户首次运行会看到 Gatekeeper / SmartScreen 提示。没有这些凭据
也能发布更新，只是安装体验差一些；macOS 证书和公证凭据的导入要另外加进
workflow。

## 数据位置

配置和图片缓存都放在各平台的标准目录里：

| 用途 | Linux | Windows | macOS |
| --- | --- | --- | --- |
| 设置 | `~/.config/mylock/config.json` | `%AppData%\mylock\config.json` | `~/Library/Application Support/mylock/config.json` |
| 配置档与模组库 | 同目录的 `profiles.json`、`library.json` | 同左 | 同左 |
| 图片缓存 | `~/.cache/mylock/images` | `%LocalAppData%\mylock\images` | `~/Library/Caches/mylock/images` |

从改名前版本升级时，第一次启动会把旧的 `deadlock-mod-manager/config.json` 复制到新位置，旧文件保留不动。旧的图片缓存不会迁移，可以直接删除。

## 兼容性

- `.dmm.json` 按原版逐字节读写，清单版本为 3
- VPK 命名规则与原版一致，包括 `pakNN_dir.vpk` 的分片和前缀规则
- `gameinfo.gi` 里使用的开始 / 结束标记与原版相同，已有的游戏配置能被识别和替换

## 项目状态

还在早期开发中，当前版本 `0.1.2`。

已实现商店、安装、启停、角色皮肤、模组更新检查、应用自更新和启动。尚未实现：

- 一次更新多个模组
- 跳过某个模组的更新
- 多余启动参数和其它启动方式

更多计划见 [`TODO/`](TODO/)。

## 致谢

- 模组数据来自 [GameBanana](https://gamebanana.com)
- 行为与文件格式对齐 Tauri 原版：[deadlock-mod-manager/deadlock-mod-manager](https://github.com/deadlock-mod-manager/deadlock-mod-manager)
- 界面框架：[Mygo](https://github.com/egoist/mygo)

MyLock 是非官方工具，与 Valve 无关。Deadlock 是 Valve Corporation 的商标。

## 许可证

[MIT](LICENSE) © 2026 BabyLy233
