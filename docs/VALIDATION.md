# 验证记录 — 2026-11-16

本机环境：Linux amd64、Go 1.26.3、Node.js 24.22.1、npm 11.9.0。

GitHub Actions：Ubuntu 与 Windows amd64，Go 1.26.3、Node.js 24。正式版代码提交 `16d60d26cb0eca96e3b218917b93f2190c756279` 的 [v0.3.0 完整验收与发布](https://github.com/doux93/sshd/actions/runs/43855180100) 三个任务全部成功。发布后未改动应用运行代码；后续补充独立的 Windows→Linux 发布包验收流程、结果与界面截图。

固定下载：[sshx v0.3.0](https://github.com/doux93/sshd/releases/tag/v0.3.0)。已验证两个 ZIP 可完整解压，只包含程序、快速开始、VERSION 和 BUILD.txt；校验文件、打包产物与 GitHub Release 公布的 SHA-256 一致。Linux 包实际运行 `--version` 输出 0.3.0 及上述提交；Windows 原生版本与本地启动在 CI 验证。

| 下载包 | SHA-256 |
|---|---|
| `sshx-Linux-amd64.zip` | `d698aa3f1328217210d0f3938a858c0abfc8254a738b591b516e49a6cc668a90` |
| `sshx-Windows-amd64.zip` | `37c29c8decd9dc4ca483973052b3ebb758c3e2b0e7992f0836f3c2e24ab442f9` |

## 已通过

| 检查 | 结果 |
|---|---|
| `go test -race -count=1 ./...` | 15 个有测试的包全部通过；含真实 OpenSSH/tmux 恢复、连接错误分类、浏览器启动地址校验及既有回归，无测试失败 |
| `go vet ./...` | 通过 |
| `npm --prefix web test` | 21 个测试通过，无失败或跳过（组件交互、状态与协议） |
| `npm --prefix web run build` | TypeScript 检查、Vite 生产构建、PWA Service Worker 构建通过 |
| `go build -o sshx ./cmd/sshx` | Linux amd64 构建通过 |
| Windows `go test -count=1 ./...`、`go vet ./...`、原生构建 | 通过；Windows Credential Manager 读、写、覆盖、删除实际执行；OpenSSH/SFTP 与 tmux 的 Unix 集成测试在 Windows 跳过，由 Linux 执行 |
| Chromium 桌面及手机尺寸 E2E | Linux 与 Windows 各 11 个有效场景通过；另 7 项是桌面/手机互不适用的组合，按设计跳过 |
| `git diff --check` | 通过 |

`sshx/web` 与 `internal/testssh` 包没有 Go 测试文件，因此 Go JSON 日志中出现该包的 `skip` 状态；这不是集成测试被跳过。前端协议测试通过 npm 单独运行。

## Windows 原生客户端 → Linux 服务端发布包实测

[独立验收运行](https://github.com/doux93/sshd/actions/runs/43858021729) 已成功，执行的是下载并校验后的 **v0.3.0 Windows 发布包**，程序版本与提交为 `sshx 0.3.0 (16d60d26cb0eca96e3b218917b93f2190c756279)`。测试流程提交为 `cca64e3312a009c1765a99d7c5729de7011496fb`。

- 客户端为 Windows Server 2025 上的原生程序、Windows Credential Manager 和 Chromium；服务端为 WSL2 Ubuntu 24.04 的真实 OpenSSH、SFTP 和 tmux。
- 10 个阶段全部通过，包括指纹确认、错误密码、草稿测试、保存密码后登录、浏览器键盘输入、中文/空格私钥路径、实际 SSH TCP 中断，以及强制结束并重启整个 Windows 工作台进程。
- 中文/空格文件名的 2 MiB 文件经浏览器上传，Linux 实际文件和下载内容的 SHA-256 均为 `c92a1eef18d94e3ad434b57657c834683284f7af85fc726b045922157caa3006`；重命名后内容仍一致。
- 初始、断网恢复、工作台进程重启恢复三个阶段，Shell PID 均为 `421`，后台测试任务 PID 均为 `465`，环境变量和工作目录一致。重启后的连接使用 Windows 保存的密码，旧工作台 Token 被拒绝。
- 结束远端终端缺少确认时被拒绝；确认后实际 tmux 会话消失。测试用户、文件、凭据及进程已执行清理。

[完整结果 JSON](evidence/windows-linux-v0.3.0.json) · [真实界面截图](images/windows-linux-recovery.png) · [验收范围与复现](WINDOWS_LINUX_ACCEPTANCE.md)

这次验收没有发现需要修改应用运行代码的问题。受控的 Windows Server + WSL2 环境不等同于用户 Windows 10/11 真机或远端物理服务器，也没有覆盖用户实际 VPN、跳板、网络延迟和安全软件。

## 0.3.0 终端恢复验收

- Linux 使用真实 OpenSSH 服务和 tmux，创建保活终端后关闭全部工作台连接、关闭数据库、重建工作台依赖并重新打开原数据库。恢复后的 Shell PID、环境变量和工作目录与关闭前一致。
- 同一主机重复连接复用当前会话；“断开并保留”后仍能恢复同一 PID；主动结束远端 tmux 后，恢复返回明确的 410，保留记录并且不创建替代 Shell。
- 结束已不存在的终端可清理记录；结束仍存在的终端实际终止对应 tmux 会话。测试只清理自身随机命名的 tmux socket。
- 恢复 API 要求认证；结束与忘记要求显式确认。主机地址变化在拨号前被拒绝；仍有关联恢复记录的主机禁止删除。
- 前端组件验证恢复只调用恢复接口、失败不回退新建、断开和结束远端任务分开确认。
- Chromium 通过真实 HTTP、临时数据库及 WebSocket 验证恢复入口、目标变化提示、取消结束不发请求、离线忘记、多浏览器列表同步，以及远端缺少 tmux 时的提示和记录清理。该部分使用固定 SSH 测试服务；真实 tmux 进程恢复由上述 Linux Go 集成测试验证。
- 本地环境缺少 tmux，因此本地该集成测试明确跳过；CI Linux 安装 OpenSSH/tmux，并设置 `SSHX_REQUIRE_TMUX_TEST=1`，依赖缺失将导致失败，不能以跳过代替通过。

## 0.2.x 新增验收

- Windows 本地启动：`--open-browser=false` 启动原生程序，未认证请求返回 401，启动地址中的 Token 能访问工作台；版本输出与 VERSION 一致。系统默认浏览器自动打开属于实现功能，未在 CI 桌面交互验收。
- 明暗主题切换及刷新记忆，切换不新增 attach/detach；左右/上下分屏各自输入，只改变目标会话目录；900px 窗口没有页面横向溢出。
- 常用命令收藏、刷新保留、剪贴板读回，复制操作不产生终端输入帧；加载时拒绝控制字符和畸形数据。
- 错误密码展示认证排查建议，修正后“保存并连接”打开实际 SSH 终端。
- 浏览器执行真实 SSH/SFTP 上传并下载核对内容；通过网络路由注入失败与延迟验证取消、重试及文件面板关闭后的队列操作。
- 上传队列单元测试验证顺序、固定目标、等待服务端保存、取消活动/等待任务、重试与保留文件、清理历史不误删待传任务。
- 手机尺寸的明暗终端输出均可见，输入仍被禁止。通知环境与权限指引已加入，真实推送到达率和系统通知点击仍需真机验收。

## 本次产品体验回归

- 实际 WebSocket 上测试 passive 和显式 readOnly 附着：伪造输入帧也不会进入后端；另一个可写附着仍可正常输入。
- 本机真实 SSH 协议测试：草稿连接成功完成密码认证、立即关闭、不创建主机、不创建会话且不打开 Shell/exec 通道。
- 旧版数据库（schema v4）升级后保留主机、主密码校验材料及 vault salt，新增告警表可用。
- 告警记录经数据库重新打开后保留确认状态，分页不重复，最多保留 1,000 条，查询和确认 API 要求认证。
- 模拟模型服务验证当前会话绑定、结构化说明、未勾选时不读取/发送终端上下文。
- React 组件测试验证独立结束确认、主机搜索与收藏、草稿测试不保存、编辑保留告警配置、AI 解释、告警确认及切换页面恢复。
- 状态与协议测试验证关闭视图不结束会话，刷新或重新加载后不自动打开已关闭视图，恢复选中标签，以及只读权限在重连后保留。
- 通知目标按主机筛选历史，验证不会把 nodeId 当作会话 ID 或显示其他主机记录。

组件测试使用 React Test Renderer；它不模拟真实布局、浏览器键盘与剪贴板行为，因此不能替代浏览器 E2E。

## 既有关键回归覆盖

- 认证：过期凭据拒绝续期、退出登录撤销数据库凭据、已建立的 WebSocket 随退出关闭、外部 Origin 拒绝、登录并发限制。
- 文件：调用本机 `/usr/lib/openssh/sftp-server` 进行真实 SFTP 协议测试，覆盖删除和重命名符号链接、悬空链接、递归目录中的链接、上传失败保留原文件、成功覆盖保留权限及临时文件清理。这些测试在本次环境中实际执行。
- 会话与网络：SSH 握手期限、输出上限、终端尺寸协商、备用屏幕识别、读取快照不延长 TTL、通道 ID 复用、会话及监控关闭。
- 数据：凭据加密 ID 绑定、无效 nonce 处理、凭据写入失败补偿回滚、会话有效期判断。
- AI：readonly/confirm 拒绝任意 Shell，trusted 的权限行为，生成命令的换行和控制字符校验。
- 前端：从接口初始化认证模式、清理旧 Token、监控对象解析、异常帧处理、回放前重置、慢订阅者重新附着、主动断开取消重连。

前一轮修订另以 Linux 二进制，在临时数据库上完成了 HTTP 冒烟检查：首次密码文件初始化和删除、HTML/CSP、认证状态、错误密码拒绝、登录与默认 Secure/HttpOnly Cookie、带加密凭据的节点保存、监控及 AI 配置保存、跨来源写入拒绝、退出后凭据失效、重启后凭据库重新锁定和节点保留，以及 SIGTERM 正常退出。

## 自动化浏览器与 Windows 验收

Playwright 使用真实生产构建、HTTP/WebSocket、临时数据库和本机 SSH 协议服务，模型响应由本机固定测试服务提供。两套操作系统均完成：

- 登录、PWA 注册与刷新导航，退出后原凭据失效。
- 草稿主机测试与指纹确认、收藏与搜索、终端输入、关闭视图后找回原会话、断网重连、结束会话确认。
- AI 结构化预览与复制；测试站点获准访问剪贴板后读回 `pwd`，确认未写入终端。
- 布局宽度记忆；桌面 WebGL 上下文丢失后退回 DOM 并继续显示原输出。
- 告警深链接筛选主机、确认后刷新保留状态。
- 手机触摸打开只读终端，收到回放且 DOM 输出可见，键盘输入不产生发送帧；页面没有横向溢出。终端自身横向滚动保留服务端 PTY 宽度。

检查实际截图时发现手机高像素密度下 WebGL 空白，已将 passive 视图改为 DOM 渲染并验证截图。Windows 测试还暴露并修复了空闲时间恰好等于 TTL 时的回收边界。

工作流报告和开发构建附件保留 14 天；正式版 ZIP、版本信息和校验值另发布到 Releases，不依赖临时附件。图形界面截图见 README。浏览器使用 Chromium；手机尺寸、触摸与像素密度模拟不等同于真机验收，也未验证完整 PWA 缓存版本升级。

## 尚未完成的实机验收

- macOS 本地钥匙串、实际 ssh-agent、真实手机及移动 Web Push、各类代理及 SFTP 服务器组合、长期运行和高并发故障压测。
- 外部 MCP 客户端兼容性；当前实现仍为基础 JSON-RPC HTTP，未提供完整 Streamable HTTP。

## 构建依赖检查

本轮将 Vite 更新至 6.4.6，并更新相关传递依赖；Workbox 的 `glob` 显式覆盖到 14.0.6，已重新验证生产构建和 Service Worker 注入。`npm audit` 当前没有中危、高危或严重项。

仍有 `deepmerge@4.3.1` 的低危原型污染通告，以及由其依赖链引出的 3 项低危记录（共 4 项）。这是 PWA 构建工具依赖，不属于发布给浏览器的应用运行依赖；上游尚无更新版本。构建使用仓库内可信配置，未采用自动降级 PWA 插件来隐藏通告。后续仍需跟踪上游修复。

## 复现

解压后进入源码根目录，先构建前端：

```sh
cd web
npm ci
npm test
npm run build
cd ..
go test -race -count=1 ./...
go vet ./...
go build -o sshx ./cmd/sshx
```

`-race` 需要对应平台的 C 工具链。OpenSSH SFTP 集成测试在缺少 `/usr/lib/openssh/sftp-server` 时会明确跳过。终端恢复集成测试需 Linux、OpenSSH 服务端、tmux 及可用的 `/run/sshd`；设置 `SSHX_REQUIRE_TMUX_TEST=1` 可强制要求它实际执行。源码包不包含依赖、二进制或前端构建输出。
