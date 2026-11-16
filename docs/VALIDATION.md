# 验证记录 — 2026-11-16

本机环境：Linux amd64、Go 1.26.3、Node.js 24.22.1、npm 11.9.0。

GitHub Actions：Ubuntu 与 Windows amd64，Go 1.26.3、Node.js 24。最终代码提交 `d1c1ee814dbac15dc622363787a1c44e839b2f76` 的 [Verify #4](https://github.com/doux93/sshd/actions/runs/43834310879) 两个任务均成功。后续仅补充文档与截图。

## 已通过

| 检查 | 结果 |
|---|---|
| `go test -race -count=1 ./...` | 15 个有测试的包全部通过；136 个顶层测试，连同子测试共 142 个通过事件，无测试失败或跳过 |
| `go vet ./...` | 通过 |
| `npm --prefix web test` | 14 个测试通过，无失败或跳过（组件交互、状态与协议） |
| `npm --prefix web run build` | TypeScript 检查、Vite 生产构建、PWA Service Worker 构建通过 |
| `go build -o sshx ./cmd/sshx` | Linux amd64 构建通过 |
| Windows `go test -count=1 ./...`、`go vet ./...`、原生构建 | 通过；Windows Credential Manager 读、写、覆盖、删除实际执行；OpenSSH Unix 路径测试在 Windows 跳过，由 Linux 执行 |
| Chromium 桌面及手机尺寸 E2E | Linux 与 Windows 各 6 个有效场景通过；另 2 项是桌面/手机互不适用的组合，按设计跳过 |
| `git diff --check` | 通过 |

`sshx/web` 包没有 Go 测试文件，因此 Go JSON 日志中出现该包的 `skip` 状态；这不是集成测试被跳过。前端协议测试通过 npm 单独运行。

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

另以本次构建的 Linux 二进制，在临时数据库上完成了 HTTP 冒烟检查：首次密码文件初始化和删除、HTML/CSP、认证状态、错误密码拒绝、登录与默认 Secure/HttpOnly Cookie、带加密凭据的节点保存、监控及 AI 配置保存、跨来源写入拒绝、退出后凭据失效、重启后凭据库重新锁定和节点保留，以及 SIGTERM 正常退出。

## 自动化浏览器与 Windows 验收

Playwright 使用真实生产构建、HTTP/WebSocket、临时数据库和本机 SSH 协议服务，模型响应由本机固定测试服务提供。两套操作系统均完成：

- 登录、PWA 注册与刷新导航，退出后原凭据失效。
- 草稿主机测试与指纹确认、收藏与搜索、终端输入、关闭视图后找回原会话、断网重连、结束会话确认。
- AI 结构化预览与复制；测试站点获准访问剪贴板后读回 `pwd`，确认未写入终端。
- 布局宽度记忆；桌面 WebGL 上下文丢失后退回 DOM 并继续显示原输出。
- 告警深链接筛选主机、确认后刷新保留状态。
- 手机触摸打开只读终端，收到回放且 DOM 输出可见，键盘输入不产生发送帧；页面没有横向溢出。终端自身横向滚动保留服务端 PTY 宽度。

检查实际截图时发现手机高像素密度下 WebGL 空白，已将 passive 视图改为 DOM 渲染并验证截图。Windows 测试还暴露并修复了空闲时间恰好等于 TTL 时的回收边界。

工作流保存 `browser-report-Linux`、`browser-report-Windows` 和对应程序 `sshx-Linux`、`sshx-Windows`，保留 14 天。图形界面截图见 README。浏览器使用 Chromium；手机尺寸、触摸与像素密度模拟不等同于真机验收，也未验证完整 PWA 缓存版本升级。

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

`-race` 需要对应平台的 C 工具链。OpenSSH SFTP 集成测试在缺少 `/usr/lib/openssh/sftp-server` 时会明确跳过。源码包不包含依赖、二进制或前端构建输出。
