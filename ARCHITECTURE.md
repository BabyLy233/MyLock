# MyLock 架构

MyLock 是 Deadlock 的模组管理器。它是一个单进程的 Go 程序：窗口由
[Mygo](https://github.com/egoist/mygo) 在 GPU 上直接绘制，界面是 Go 代码，
不加载网页，也没有 WebView。

这份文档说明代码怎么分层、各部分如何协作，以及哪些约束不能破坏。功能说明和
使用方式见 [README.md](README.md)，待办见 [`TODO/`](TODO/)。

## 目录与分层

```text
main.go                  命令入口，只调用 app.Run
internal/
  app/                   窗口、页面、应用状态与后台任务的编排
  addons/                已安装模组的清单、VPK 命名、启停、配置档
  archive/               zip / rar / 7z 解包
  config/                设置、库、配置档的持久化
  download/              模组压缩包下载（断点续传、校验）
  game/                  游戏安装检测、Steam、gameinfo.gi、启动、进程
  gb/                    GameBanana API 客户端
  heroes/                角色名归一化、别名表与分类 / 名字解析
  imagecache/            图片的磁盘缓存
  vdf/                   Valve KeyValues 解析
  about/                 版本号与 User-Agent
```

依赖只有一个方向：`app` 依赖所有领域包，领域包之间只有三处依赖，其余互不
相识。

```text
main ──> app ──> addons, archive, config, download, game, gb, imagecache

领域包之间：
download ──> about
gb       ──> about
game     ──> vdf
```

| 包 | 职责 | 不做什么 |
| --- | --- | --- |
| `app` | 页面、路由、用户操作、应用状态、后台任务编排 | 不实现文件格式和网络协议 |
| `addons` | `.dmm.json`、VPK 命名、启用/禁用、配置档目录 | 不启动游戏，不联网 |
| `archive` | 安全解包 | 不认识 VPK，不决定装到哪 |
| `config` | 三个 JSON 文件的读写与原子替换 | 不持有运行时状态 |
| `download` | 下载、续传、大小 / MD5 校验 | 不解包，不安装 |
| `game` | 找游戏、读 Steam VDF、改 `gameinfo.gi`、交给 Steam 启动、查进程 | 不读写模组清单 |
| `gb` | 目录、搜索、分类、详情、批量更新查询、文件服务器列表 | 不下载文件正文，不画界面 |
| `heroes` | 把名字、别名和 Skins 分类归一成角色 | 不读 API，不决定有哪些角色 |
| `imagecache` | 图片字节的磁盘 LRU | 不解码图片 |
| `vdf` | KeyValues 文本解析 | 不解释 Steam 的语义 |
| `about` | 版本、提交号、UA | — |

`app` 是唯一接触 Mygo UI、操作系统对话框和窗口的包。领域包是普通 Go 代码，
可以单独测试，也可以被将来的其它前端复用。

## 进程生命周期

`main.go` 只有入口。真正的工作在 `app.Run()`（`internal/app/app.go`）：

1. 从构建信息取版本号，写入 `about.Version`。
2. 注册原生更新插件（`plugins/updater/native`），把它的检查和偏好接到
   设置页的 `updaterHooks`。
3. 读取设置（`config.Path` / `config.Load`），必要时从改名前的目录迁移。
4. 打开图片磁盘缓存（`imagecache.Path` / `imagecache.Open`，预算 128 MiB）。
   没有缓存目录时只用内存缓存。
5. 在窗口出现前确定外观（浅色 / 深色 / 跟随系统），保证第一帧就是用户选的
   主题。
6. `mygo.App.WhenReady` 创建窗口，把 `ui.View(a.view)` 设为内容。
7. `a.start` 读取配置档与库文件，然后在后台定位游戏并读取当前配置档。
8. 启动 `watchGame` 协程，每 2 秒看一次游戏是否在运行。

窗口关闭后 `mygo.App.Run()` 返回。

## 线程模型

Mygo 的界面在主线程上构建，`app.view` 会在每次需要新帧时、每个事件之后重建
整棵 UI 树。由此得到两条硬性规则：

- **只有主线程读写界面状态和控件绑定。** `pathInput`、`list`、`deleteAsk`、
  `store` 的查询条件、分页、选中项等，都只属于主线程。
- **耗时工作一律在协程里做。** 找游戏、读配置档、请求 GameBanana、下载、
  解包、安装、更新、图片抓取、进程轮询，都在后台协程。

后台协程不能直接改界面。结果通过 `app.publish` 交回主线程，再调用
`app.invalidate` 请求重绘。窗口尚未创建时（测试就是这样跑的），`publish`
直接同步执行。

视图读取共享状态时使用**快照**：`state()`、`modsState()`、`updatesState()`、
`store.shown` 等在一次加锁中把需要的字段复制出来，界面只在快照上作判断。
这样一帧内看到的是同一个时间点，也避免渲染过程中持有锁。

### 锁

| 锁 | 保护什么 |
| --- | --- |
| `app.mu` | 后台写入、视图读取的所有应用状态 |
| `app.settingsMu` | `config.json` 的整文件替换 |
| `app.profilesMu` | `profiles.json` 的整文件替换 |
| `app.libraryMu` | `library.json` 的整文件替换 |
| `app.gameinfoMu` | 「读当前配置档 + 写 `gameinfo.gi`」作为一个事务 |
| `game.writeMu` | `gameinfo.gi` 的读-改-写本身 |
| `store.mu` | 目录请求带回的数据；`seq` 用来丢弃过期回复 |
| `imagecache` / `images` 内部锁 | 图片缓存与并发抓取 |

`gameinfoMu` 和 `game.writeMu` 是两层：前者保证写进文件的搜索路径属于同一个
配置档，后者保证两个补丁不会互相覆盖。任何一个被删掉，都会留下游戏加载的
路径与配置档不一致的窗口。

## 页面与路由

`app.router` 有四个页面，左侧栏负责切换：

| 路由 | 页面 | 主要状态 |
| --- | --- | --- |
| `/mods` | 模组管理 | 模组列表、开关、角色指定、配置档、删除确认、更新面板 |
| `/store` | 模组商店 | 目录、搜索、分类、详情、文件选择、安装进度、皮肤冲突确认 |
| `/skins` | 角色皮肤 | 角色列表、搜索、选中角色、皮肤网格、删除确认 |
| `/settings` | 设置 | 游戏目录、检测、主题、皮肤互斥、图片缓存、应用更新、版本信息 |

商店页内部还有一层状态：条目种类（模组 / 音效 / 制作中）、排序、分类与英雄
筛选、页码、搜索词、隐藏 NSFW、详情面板、截图查看器。它们都在 `store` 结构
里，只有主线程修改；请求结果由 `store.mu` 保护并用序号对齐。

## 核心流程

### 定位游戏

`game.Resolve(pinned)`：

- 用户手动指定过目录时，`Validate` 只检查它是否包含
  `game/citadel/gameinfo.gi`，不悄悄替换。
- 否则 `Detect` → `FindSteam`（按平台列出 Steam 根目录，包括 Flatpak、Snap
  和 Debian 包）→ 读 `libraryfolders.vdf` → 在每个库里找
  `steamapps/common/Deadlock` → 从 `appmanifest_1422450.acf` 读 build id。

`look` 协程在同一次读取里同时得到：游戏位置、当前配置档的模组、`gameinfo.gi`
状态、磁盘上的配置档列表。这样切换目录时，界面不会先显示旧目录的模组。读完后
`fillModInfo` 再在后台补齐商店里才有的名字和缩略图。

### 安装模组

1. 用户在商店详情页勾选一个或多个下载文件（同一模组的变体和附加包可以一起
   安装）。
2. `a.downloadURL` 把 API 给的地址换成用户选的 GameBanana 文件服务器地址
   （默认不换，见下面的「下载路由」）。
3. `download.Client.Get` 把每个文件下载到临时位置：先写 `.partial`，断点
   续传，校验 API 给出的大小与 MD5，完整后才改名到目标路径。重定向逐跳检查
   域名。
4. `archive.Extract` 把每个压缩包解到自己的临时目录。解包按不可信输入处理：
   拒绝绝对路径、`..` 穿越、链接和不可用的文件名，权限固定为 0644 / 0755。
5. `addons.FindArchives` 找出所有 VPK，并按压缩包名、目录、文件名生成统一
   的安装名。同一个包里多个 `pak01_dir.vpk` 不会互相覆盖，命名结果与原版
   Tauri 应用一致。
6. `addons.Profile.Install` 复制文件进配置档目录并写 `.dmm.json`。清单最后
   写；中途失败会把已复制的文件撤掉。
7. 安装成功后写 `library.json`：模组的名字、作者、分类、缩略图、页面地址、
   安装时间，以及**安装时选中的文件 ID**。后者是更新检查的依据。
8. 如果是启用安装，再走一遍「启用」并把搜索路径写进 `gameinfo.gi`。

下载和更新在后台协程里跑，用户可能已经离开开始它的页面：窗口顶部有一条全局
状态条（`app.progressBanner`），同时显示正在安装的模组（字节数和百分比）和
正在更新的模组（第几个文件），可以点「查看」回到对应页面。两条进度各自从
`store` 和 `app` 的快照里读，状态条不参与页面布局之外的任何状态。

### 下载路由

设置页的「下载路由」决定从哪台 GameBanana 文件服务器取文件：

- `gb.Fileservers` 读 `Util/Fileservers`，按原版规则丢掉不是纯主机名的
  `_sDomain`，并在 `gb.Client` 里缓存 6 小时；刷新按钮强制重读。
- `gb.PickFileserver` 按偏好挑：`default` 用 API 给的地址；`auto` 取最近
  一小时吞吐最高的 `up` 服务器；其余值当作服务器 id，存在的就用（`down` 也
  用，`terminated` 不用）。
- `a.downloadURL` 拼 `https://<domain>/<mods|sounds|wips>/<文件名>`，文件名是
  API 的 `_sFile`。挑不到、列表读失败、或者服务器已不在列表里时，回到 API
  给的地址，安装不会因此失败。
- 安装和更新两条下载路径都经过它；偏好和延迟记在 `config.json`。

### 启用、禁用与 shard

模组的启用状态不是另一份文件，而是文件本身的位置和名字：

- 禁用：文件带模组 ID 前缀，留在配置档目录里，例如
  `12345_pak01_dir.vpk`。
- 启用：文件被移动到 shard 目录并改名为 `pakNN_dir.vpk`，`NN` 决定加载
  顺序。
- 每个 shard 最多 99 个 VPK，一个配置档最多 10 个 shard（`addons2` 到
  `addons10`）。一个模组的所有文件始终在同一个 shard 里。
- 游戏只认 `gameinfo.gi` 的 `SearchPaths`：`addons` 里放着 VPK 并不足以
  让游戏加载，必须把对应目录写进去。`addons.Profile.SearchPaths` 计算当前
  需要搜索的 shard 目录，`game.PatchGameInfo` 写入带标记的段落。

因此启用/禁用是一组文件移动加一次 `gameinfo.gi` 更新，不复制大文件。游戏
运行期间禁止装卸和启停：进程每 2 秒检查一次，真正移动文件前还会再问一次。

### 配置档

- 默认配置档就是 `game/citadel/addons` 目录本身，没有独立文件夹。
- 具名配置档是 `addons/<folder>` 子目录，各自有独立的 `.dmm.json`、VPK 和
  shard。
- `profiles.json` 只记录名字、描述和时间；目录存在于磁盘但没有记录时，扫描
  会把它补录进来；记录还在但目录没了，界面显示为缺失而不是直接删掉。
- 切换配置档会切换当前目录、重新读取模组，并重写 `gameinfo.gi` 的搜索路径。

### 角色归属与皮肤互斥

- 角色名来自 `library.json` 里的手动覆盖、Skins 分类的子分类，或模组标题里的
  英雄别名（`heroes` 包）。分类树仍是角色列表的来源，内置名单只在读不到
  API 时兜底、以及给没有分类的标题猜角色时使用。
- 「角色皮肤」页把当前 profile 里属于 Skins / Model Replacement 的模组按角色
  分组；没有元数据的模组归到「通用/其他」，可以在「模组管理」每行的「角色…」
  里指定，覆盖写回 `library.json`。
- 一个角色默认只穿一套皮肤：穿上新的会先停用同角色的其它皮肤（文件保留），
  再点当前这套就是脱下。设置里打开「允许同一角色同时启用多套皮肤」后变成
  逐套开关；多于一套时角色列表标冲突，但不会自动改动。
- 从商店安装皮肤时，如果该角色已有启用中的皮肤，先弹「取消 / 两者都留 /
  交换」。交换在下载、解包完成后、安装新文件前重新读取 profile，停用旧的再
  安装；「安装时提醒角色冲突」可以在设置里关掉。

### 检查更新

- 打开「模组管理」时自动检查，结果在 10 分钟内有效；也可以手动触发。
- `gb.Updates` 通过旧 API 一次最多询问 50 个投稿，返回页面修改时间和全部
  文件（包括归档文件）。
- 判断规则：
  - 应用安装的模组只比较安装时选中的那些文件。作者只改说明不会误报；某个
    被选中的文件消失（被替换）也算更新。
  - 没有文件记录、由原版或用户手动安装的模组，退回到比较磁盘文件时间和
    页面时间。
- 更新时重新下载安装时选中的**全部**文件，用 `addons.Profile.Replace` 先
  移开旧文件再放新文件，失败回滚；完成后恢复原来的启用状态。

### 应用更新

- 应用本身用 Mygo 自带的签名更新：`mygo.json` 的 `updates` 给出公钥和
  GitHub 仓库，`mygo build` 为每个平台生成签名包、delta 和
  `update-<target>.json`，发布在 `v<版本>` 的 GitHub Release 里。
- 运行时注册 `plugins/updater/native`：它启动后自动检查、每天一次，发现新
  版本时打开一个同样由 Mygo 绘制的更新窗口，显示更新说明和下载进度。
- 设置页的「应用更新」一节通过 `updaterHooks`（`appupdate.go`）读插件的
  偏好和上次检查时间，提供手动检查与两个自动更新开关。`updaterHooks` 的零值
  表示这个构建没有更新器（测试、未启动的窗口），此时只说明不能更新的原因，
  不调用插件的任何函数。
- 版本号只有 `mygo.json` 一个来源：`Run` 把它写进 `about.Version`，界面和
  User-Agent 都读这一个值。

### 启动游戏

两种启动都只改 `gameinfo.gi`，启动请求交给 Steam（`steam://run/1422450//`）：

- **模组版**：写入当前配置档的搜索路径，然后交给 Steam。
- **原版**：写入游戏自带的搜索路径并移除本应用的标记。启动意图
  （`vanillaPending` / `vanillaSeen`）会持久化到 `config.json`，所以即使应用
  在游戏运行期间退出，下次启动仍能知道要在游戏结束后恢复模组。
- 启动器只等待约 8 秒，等它交出请求或报告失败；游戏进程归 Steam 管理，应用
  不等待它。
- 游戏从运行变为退出时，如果上一局是原版启动，`restoreMods` 把当前配置档的
  搜索路径写回去。

### 图片

商店的缩略图和截图是同一个地址的两个尺寸。`images` 以「URL + 目标尺寸」为
键，在后台抓取并按目标尺寸解码；原始字节同时交给 `imagecache` 写到磁盘。
磁盘缓存上限 128 MiB、最多 20000 张，按文件的修改时间做 LRU（这样重启后
顺序仍然有效），键是 URL 的哈希。图片超过 4 MiB 不缓存。设置页可以查看用量
并清空。

## 持久化

### 应用数据

| 文件 | 位置 | 内容 |
| --- | --- | --- |
| `config.json` | 平台配置目录下的 `mylock/` | 游戏目录、当前配置档、主题、原版启动状态 |
| `profiles.json` | 同上 | 具名配置档的列表 |
| `library.json` | 同上 | 按模组 ID 记录的商店信息、安装时间、安装文件与手动角色覆盖 |
| `images/` | 平台缓存目录下的 `mylock/` | 图片字节，可随时删除 |

所有 JSON 文件都通过「写临时文件再 rename」原子替换，不会留下半个文件。

### 游戏目录

```text
<游戏目录>/game/citadel/
├── gameinfo.gi                 搜索路径；本应用只改标记段
├── gameinfo.gi.bak             第一次改动前保留的原文件
├── addons/                     默认配置档
│   ├── .dmm.json               该配置档的清单
│   ├── pak01_dir.vpk …         已启用的模组文件
│   ├── <modid>_*.vpk           已禁用（带前缀）
│   └── <profile>/              具名配置档
│       ├── .dmm.json
│       └── …
├── addons2/<profile>/…         同一配置档启用文件超过 99 个时的溢出 shard
└── addons3 … addons10
```

应用只写 `addons`、`addonsN` 和 `gameinfo.gi`（含 `.bak`），不碰游戏本体。

## 兼容性约束

以下内容与 Tauri 原版或已发布的数据格式绑定，改动会破坏互操作：

- `.dmm.json` 的字段、版本号 3、固定临时文件名 `.dmm.json.tmp`，以及
  `Load` 在崩溃后从临时文件恢复的行为。
- VPK 命名规则：`pakNN_dir.vpk` 的顺序、禁用时的模组 ID 前缀、多压缩包时的
  变体限定名。两边必须对同一个文件得出同一个名字。
- `gameinfo.gi` 的开始 / 结束标记，以及原版搜索路径的内容。
- 只改游戏目录中上面列出的文件。

新增字段应遵守现有格式的读写方式（未知字段的保留策略以原版为准），不要
「顺手整理」JSON 的键顺序或空值。

## 安全与失败处理

- **压缩包是不可信输入**：解包前检查每个路径，只写入指定目录；拒绝链接；
  不信任权限位。
- **下载地址受限**：只从 `gamebanana.com` 和 `deadlockmods.app`（含跳转）
  取文件；跳转逐跳检查。文件服务器的子域名来自 API，但只有字母、数字和 `-`
  的记录会被采用，再固定拼上 `.gamebanana.com`，所以列表本身指不到别处。
- **完整性校验**：API 给了大小和 MD5 就校验；不符会删除半成品，续传也救不
  回来。
- **可回滚的文件操作**：安装和更新先记录已写文件，任何一步失败都撤掉；清单
  最后写。`gameinfo.gi` 改动前保留 `.bak`，并拒绝写入自己校验不通过的内容。
- **界面不崩溃**：错误以字符串状态进入快照，显示在对应位置；后台协程失败
  不会 panic 主线程。
- **游戏运行保护**：运行期间不允许移动任何 VPK；移动前再检查一次，而不是
  相信上一次轮询。

## 构建与测试

```bash
make build   # 当前平台
make         # 全部平台安装包
make check   # go vet + mygo vet + 全部测试
make dev     # 热重载开发
```

Mygo 的版本固定在 `go.mod` 的 tool 指令里，通过 `go tool mygo` 调用。
`make vet` 对 `internal/app` 运行 `mygo vet`，因为界面代码现在都在那里；
根包只剩入口。

测试按层组织：

- 领域包（`addons`、`archive`、`config`、`download`、`game`、`gb`、
  `imagecache`、`vdf`）是普通单元测试。网络用 `httptest`，文件用临时目录，
  `download` 还能在测试中缩短重试间隔。
- `app` 包测试在同一个包内，直接构造 `app`，在没有窗口的情况下运行视图
  （`ui` 的测试视图允许像用户一样点击），用 `waitFor` 等后台协程发布结果。
  `launch_test.go` 用一个假的 Steam 启动脚本来读回 `steam://` 地址。
  `game.running` 是字段而不是直接调用，测试可以替换进程检测的结果。

## 扩展指引

- 新的**领域能力**（新的 API、新的文件格式、新的平台适配）放进
  `internal/<name>`，不要放进 `app`；`app` 只负责把它接到界面上。
- 新的**页面**加在 `app` 的 router 和左侧栏；页面状态放进 `app` 或对应子
  结构，遵循「主线程改，后台结果经 `publish` 回来」。
- 新的**持久化字段**要同时考虑旧文件缺字段时的行为，以及是否属于上面列出的
  兼容格式。
- 任何会移动 VPK、改 `gameinfo.gi` 或替换清单的操作，都要先确认游戏没在
  运行，并为中途失败准备回滚。
