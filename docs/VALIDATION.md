# 验证记录 — 2026-11-16

环境：Linux amd64、Go 1.26.3、Node.js 24.22.1、npm 11.9.0。

## 已通过

| 检查 | 结果 |
|---|---|
| `go test -race -count=1 ./...` | 15 个有测试的包全部通过；136 个顶层测试，连同子测试共 142 个通过事件，无测试失败或跳过 |
| `go vet ./...` | 通过 |
| `npm --prefix web test` | 14 个测试通过，无失败或跳过（组件交互、状态与协议） |
| `npm --prefix web run build` | TypeScript 检查、Vite 生产构建、PWA Service Worker 构建通过 |
| `go build -o sshx ./cmd/sshx` | Linux amd64 构建通过 |
| `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o sshx.exe ./cmd/sshx` | Windows amd64 交叉编译通过；未在 Windows 上执行 |
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

新增 Playwright 桌面和移动尺寸用例，使用真实生产构建、HTTP/WebSocket、临时数据库和本机 SSH 协议服务，覆盖登录退出、连接测试、会话保留与恢复、掉线重连、AI 预览、布局记忆、告警深链接及确认、移动端只读。新增 Windows Credential Manager 写入/读取/覆盖/删除原生测试。

工作流 `.github/workflows/verify.yml` 在 Linux 和 Windows 执行这些检查，保存浏览器报告、截图及通过验收的程序。上传后的运行结果以 GitHub Actions 为准，不能把测试脚本已添加等同于测试已通过。

## 尚未完成的实机验收

- 完整浏览器 E2E：尝试启动 Chromium 时被当前环境的 socket 权限限制（EPERM）阻止。前端构建与协议测试通过不等于页面交互、真实 PWA 缓存切换已完成浏览器验收。
- Windows/macOS 本地钥匙串、实际 ssh-agent、真实移动 Web Push、各类代理及 SFTP 服务器组合、长期运行和高并发故障压测。
- 外部 MCP 客户端兼容性；当前实现仍为基础 JSON-RPC HTTP，未提供完整 Streamable HTTP。

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
