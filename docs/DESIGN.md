> 历史设计文档：包含尚未实现或已调整的方案。当前行为、权限和限制以 README.md、DEPLOYMENT.md、CHANGELOG.md 为准。

# 一个人的服务器控制台 — 设计架构与实施计划

> **版本**:v3(2026-08-05 收敛到个人场景)
> **状态**:M1–M4 已交付并测试通过;M5–M10 待实施。
> **v3 相对 v2 的变化**:删除团队协作与多用户体系;常开(remote)模式从最后一个里程碑提为**主形态**;AI 三形态优先级反转(Ctrl+K 与 MCP 优先,内置 Agent 最后或不做);新增告警;明确"不做"清单。

---

## 〇、产品定位与原则

### 一句话定位

**一个人的服务器控制台**:装在一台常开的机器上,浏览器和手机都连它。
**会话不断、指标常看、异常主动找你、AI 能替你上去看一眼。**

### 目标用户 / 非目标

- **是**:管 5–50 台 Linux 的个人开发者 / 独立运维,自己的 VPS、家里的小主机、几台业务机。
- **不是**:需要账号体系、命令级授权、合规审计的企业堡垒机场景(JumpServer / Teleport 的地盘)。
- **不是**:管理数千台的规模化运维(Ansible / SaltStack 的地盘)。

明确非目标的收益:**不需要 users 表、RBAC、审批工作流、租户隔离**。整个产品是单账号的。

### 竞争对手与绕开方式

| 对手 | 它的结构性缺陷 | 我们如何绕开 |
|---|---|---|
| FinalShell | 桌面应用,不常开;没有手机端;凭据近似明文 | 常开服务端 + PWA + 钥匙串/加密 vault |
| tmux + ssh | 没有监控、没有文件面板、没有手机端 | 三者都内建,且共用同一条 SSH 连接 |
| 宝塔 / 1Panel | 要在每台机装 agent,只管单机,面板本身是攻击面 | 免 agent,一处纵览全部主机 |
| Termius / Tabby | 会话绑定本地客户端进程 | 会话是服务端一等公民 |

### 可测量的产品指标(M1–M4 也要回头受此检验)

| 指标 | 目标 |
|---|---|
| 冷启动到可输入 | < 1s |
| 新建连接到出现 shell prompt | ≤ 原生 `ssh` + 200ms |
| 按键回显延迟 | ≤ 网络 RTT + 16ms |
| `cat` 100MB 日志 | UI 不冻结;50MB/s 输出下保持 60fps,或明确降级(丢帧不丢内容) |
| 10 个活跃会话常驻内存 | < 200MB(直接对标 FinalShell 的第一痛点) |
| 断网 30s 后恢复 | 用户零操作,屏幕状态正确 |
| 首次启动到第一个连接可用 | < 60s(靠 `~/.ssh/config` 导入向导) |
| 可点击元素最小边 | ≥ 28px(WCAG 下限 24px) |
| 终端字号 | 默认 15px,`Ctrl+=` / `Ctrl+-` / `Ctrl+0` 可调并持久化 |

这些不是"性能优化项",是**产品是否成立的判据**。任何里程碑不得让它们退化。

> 后两条是踩出来的:标签关闭按钮曾经只有 12×16px、树操作按钮 16×16px,鼠标点不中;
> 终端字号写死 14px 且不可调。写新组件时按 28px 起做点击区,别再用 `text-xs` 当按钮。

---

## 一、Context

用户分析了 FinalShell(Java Swing 单体应用,痛点:内存大、终端渲染弱、密码近似明文、不兼容 OpenSSH 生态、闭源无扩展),想做一个更好用的现代 SSH 工具。竞品调研见 [finalshell-analysis.md](./finalshell-analysis.md)。

### 核心洞察

**服务端持久会话是整个项目的架构基座**,但它只在 sshx **跑在常开机器上**时才真正兑现:

- 笔记本合盖 / 休眠 → SSH 的 TCP 连接必死,ring buffer 救不了;
- sshx 进程退出 → 内存里的会话池全没。

所以桌面模式(`--local`)得到的是"**跨标签页、刷新页面不断线**",而不是 tmux 级的持久性。
**tmux 级持久性 = 常开模式(`--remote`)。这是主形态,不是后期扩展。**

由此,会话池有两种消费方式(v2 的"三种"里,团队那一路已删除):

| 消费者 | 形态 | 价值 |
|---|---|---|
| 你(多设备) | 笔记本浏览器 / 手机 PWA 同看一个会话池 | 合盖、换设备、断网都不掉线 |
| AI | MCP 让**你已有的** AI 客户端读写活跃会话 | 不开第二个 SSH 连接,直接操作现场 |

### 对 FinalShell 的取舍

**继承**:终端↔SFTP 联动、树形连接管理、免 agent 实时监控、多会话命令广播。
**修复**:凭据安全(钥匙串 / 主密码加密)、OpenSSH 生态兼容、可扩展性。
**超越**(它结构上做不到的):常开 → 后台监控 + **主动告警到手机**;开放 MCP 接口。

---

## 二、整体架构

### 2.1 进程模型:一个 Go 二进制,两种模式

```
┌─────────────────────────────────────────────────────┐
│                  sshx (单一 Go 二进制)                │
│                                                     │
│  ┌───────────┐  ┌──────────┐  ┌─────────────────┐  │
│  │ HTTP/WS   │  │ Session  │  │  Tool Layer     │  │
│  │ Server    │──│ Manager  │──│  + Guard        │  │
│  │ (embed UI)│  │ (SSH池)  │  │  → MCP Server   │  │
│  └───────────┘  └──────────┘  └─────────────────┘  │
│  ┌───────────┐  ┌──────────┐  ┌─────────────────┐  │
│  │ Config    │  │ Vault    │  │ SFTP / Forward  │  │
│  │ Store     │  │ (凭据)   │  │ Monitor / Alert │  │
│  └───────────┘  └──────────┘  └─────────────────┘  │
└─────────────────────────────────────────────────────┘
        ▲                    ▲                  ▲
   桌面模式(次要):      常开模式(主形态):     外部 AI:
   本机试用/单机        VPS/小主机/NAS       MCP over HTTP
   127.0.0.1 + token   笔记本浏览器+手机PWA  Claude Code / 手机 AI
```

- **`sshx serve --remote`(主形态)**:跑在常开机器上。**只监听内网/tailnet 接口**(见 §4.5),单账号主密码登录;凭据用主密码派生密钥(argon2id)加密。SSH 会话在服务端持久保持,浏览器关闭不掉线,重连 reattach。
- **`sshx serve --local`(次要)**:桌面模式。监听 `127.0.0.1:随机端口`,首次启动生成一次性 token 注入 UI,免登录;凭据走系统钥匙串。适合"先在本机试一下"和纯单机使用。
- 前端静态资源用 Go `embed` 打进二进制,部署 = scp 一个文件 + systemd unit。
- **桌面壳(Wails):降级为可选,可能不做**。主形态是浏览器 + PWA,套壳收益很低。

### 2.2 模式差异的实现方式

差异点全部收敛到两个接口,其余代码完全共享:

- `Authenticator` 接口:`local`(loopback + token 直通)/ `remote`(单账号主密码登录 + session cookie)
- `Vault` 接口:`keyring`(系统钥匙串,zalando/go-keyring)/ `encrypted`(argon2id 派生密钥 + AES-256-GCM)

> ⚠ 常开机器是 headless 的,**系统钥匙串不可用**(Linux 上 go-keyring 依赖 Secret Service / dbus)。
> 所以主形态下只有 `encrypted` 一条路,`keyring` 只服务桌面模式。

---

## 三、技术选型

### Go 后端

| 组件 | 选型 | 理由 |
|---|---|---|
| SSH 客户端 | `golang.org/x/crypto/ssh` | 官方库,成熟稳定 |
| SFTP | `github.com/pkg/sftp` | 事实标准,与 x/crypto/ssh 配套 |
| ssh_config 解析 | `github.com/kevinburke/ssh_config` | 导入 `~/.ssh/config` |
| ssh-agent | `golang.org/x/crypto/ssh/agent` | Windows 下支持 OpenSSH agent 命名管道 |
| 虚拟终端(AI 读屏) | `github.com/hinshun/vt10x` | 服务端维护终端屏幕状态,供 AI 读取渲染后文本 |
| Web 框架 | 标准库 `net/http` + `chi` 路由 | 轻;避免重框架 |
| WebSocket | `github.com/coder/websocket` | 轻量、维护活跃、context 友好 |
| 存储 | SQLite(`modernc.org/sqlite` 纯 Go 免 CGO) | 交叉编译无痛 |
| 系统钥匙串 | `github.com/zalando/go-keyring` | 桌面模式;跨平台 |
| 密钥派生/加密 | `golang.org/x/crypto/argon2` + AES-256-GCM | 常开模式凭据加密 |
| MCP Server | `github.com/modelcontextprotocol/go-sdk` | 官方 Go SDK,stdio + Streamable HTTP |
| LLM 调用 | 自建薄封装,直接 HTTP 调 Anthropic/OpenAI | 仅 Ctrl+K 需要单次调用,不引框架 |

### 前端

| 组件 | 选型 | 理由 |
|---|---|---|
| 框架 | React + TypeScript + Vite | 生态最大,xterm.js 集成资料多 |
| 终端 | `@xterm/xterm` + `addon-webgl`(fallback canvas)+ `addon-fit` + `addon-search` + `addon-web-links` + `addon-unicode11` | GPU 渲染,性能天花板高 |
| UI 组件 | Tailwind CSS + shadcn/ui | 快速做出现代深色 UI,PWA 响应式好做 |
| 状态 | zustand | 轻量,够用 |
| 文件树/连接树 | 自绘(shadcn Tree 模式) | 拖拽分组、右键菜单,自控更好 |
| 图表(监控) | recharts | 轻量折线图 |
| 远程文件编辑 | Monaco Editor | 浏览器里改配置文件并回传(替代 FinalShell 的"本地编辑器打开") |
| PWA | vite-plugin-pwa | manifest + service worker + Web Push |

---

## 四、关键设计

### 4.1 会话与终端模型(已对齐现有实现)

```
Connection(连接配置) --1:N--> Session(SSH 会话,服务端对象)
Session --1:1--> Terminal(交互终端 + 环形回放缓冲 + 多端 fan-out)
Session --0:N--> Exec(短命令通道,不污染交互终端)
Session --0:1--> Files(SFTP 客户端)
Session 状态机: active → closed
```

> 修正 v2:文档曾写 `Session --1:N--> Channel`,与实现不符。实现里一个 Session 恰有一个
> `*Terminal`(`internal/session/session.go`),WS 协议里的 `channelId` 只是**单条 WS 连接内的复用编号**,
> 不是会话级对象。v3 采用实现的模型。

- **持久会话**:WS 断开 ≠ 会话结束。Session 归 SessionManager 所有,WS 只是"附着"。重连后凭 sessionId reattach。
- **回放 buffer**:每个 terminal 维护环形缓冲(默认 1MB 原始字节流),reattach 时先重放再接实时流。
- **⚠ 回放 buffer ≠ scrollback**:前者是"重连时恢复屏幕"的服务端缓冲,后者是"往上翻历史"的用户功能。
  scrollback 由前端 xterm 承担(目标 ≥ 10000 行)+ `addon-search` 全量搜索。两者不要混为一谈。
- **空闲策略**:无客户端附着的会话默认保持 24h(可配),SSH keepalive 30s 防 NAT 超时。
- **Actor 标识**:每次 attach 携带 `{type:"human"|"ai", id}`,仅用于 audit 打标(个人场景无权限含义)。

#### 必须新增:`Backend.Exec`

现有 `Backend` 接口(`internal/session/backend.go`)只有 `OpenTerminal / OpenFiles / Close`。
**M5 的第一件事是加 `Exec`**,因为三个后续功能全压在它上面:AI 的 `run_command`、监控采集、命令广播。

```go
type Backend interface {
    Kind() string
    Target() string
    OpenTerminal(cols, rows int) (TerminalIO, error)
    // Exec 在连接上开一条独立通道执行命令,不影响交互终端。
    Exec(ctx context.Context, cmd string) (stdout, stderr []byte, exitCode int, err error)
    // ExecStream 用于长驻采集循环,调用方关闭 io.Closer 即终止。
    ExecStream(ctx context.Context, cmd string) (io.ReadCloser, error)
    Close() error
}
```

> **sshd `MaxSessions` 约束**:默认 10,加固过的机器常调到 1–2。终端 + SFTP + 监控 + 广播都占 channel。
> 对策:监控用**一条长驻 channel**(见 §4.6)而非每 tick 一条;Exec 失败时降级并在 UI 提示,不无声重试。

#### 多端尺寸协商(✅ 已实现)

> 原缺陷:`internal/server/ws.go` 的 attach 无条件 `term.Resize(...)`,
> 手机一附着就把笔记本上的 vim 压成手机宽度。已修复。

`attach` 帧携带 `sizeMode`:

- `negotiate`(**默认**):参与尺寸协商。PTY 尺寸取所有 negotiate 客户端视口的**最小值**
  (列、行各自取最小),任何一端都不会有内容溢出屏幕。
- `passive`:**从不改变 PTY 尺寸**,按 PTY 的实际几何渲染,本地滚动。

> ⚠ `passive` 必须由移动端布局(M9)**显式传入**,不要用媒体查询嗅探:
> `pointer: coarse` 在触屏笔记本/一体机上同样成立,会把 PC 误判成手机,
> 结果终端停在 80×24 不铺满窗口。这个坑已经踩过一次。

两条配套规则:

1. **PTY 尺寸由服务端权威下发**(`size` 控制帧):客户端必须按下发的尺寸渲染,而不是按自己的视口——
   否则别的客户端把 PTY 压小时,本端所有折行位置都是错的。attach 时下发一次,协商变化时再下发。
2. **最后一个客户端离开后 PTY 保持原尺寸**,不回弹到默认值,重连的客户端 replay 进同一几何。

### 4.2 WebSocket 协议

单一 WS 端点 `/ws`,JSON 控制帧 + 二进制数据帧混合(protocol v1):

```
控制帧(JSON text frame):
  {type:"attach", sessionId, channelId, cols, rows, sizeMode:"negotiate"|"passive"}
  {type:"resize", channelId, cols, rows}
  {type:"detach", channelId}
  {type:"attached", channelId, sessionId}
  {type:"size", channelId, cols, rows}              // 服务端权威 PTY 尺寸
  {type:"channel-closed", channelId}
  {type:"session-event", sessionId, event:"created"|"closed"}
  {type:"cwd", sessionId, cwd}                      // OSC 7 触发
  {type:"metrics", sessionId|nodeId, sample}        // 监控采样点
  {type:"alert", nodeId, level, message}            // 告警
  {type:"confirm-request", ...}                     // AI 危险操作确认
  {type:"error", channelId, message}
二进制帧(binary frame):
  [1字节 channelId][载荷]  → 终端输入/输出原始字节,多会话复用同一 WS
```

SFTP 不走 WS,走 REST(`/api/files/list|download|upload|...`),上传下载用 HTTP streaming。

### 4.3 数据模型(SQLite)

```sql
nodes(id TEXT PK, parent_id, kind 'folder'|'connection', name, sort_order,
      config JSON,         -- host/port/user/authType/proxyJump/encoding/env
                           --  + monitor:{enabled,interval}
                           --  + alerts:[{metric,op,threshold}]
                           --  + aiPolicy:'readonly'|'confirm'|'trusted'
      credential_ref TEXT, -- 指向 vault 的引用,绝不存明文
      created_at, updated_at)
credentials_meta(id PK, kind 'password'|'key'|'key_passphrase', label, created_at)
      -- 桌面模式:secret 在系统钥匙串,key = "sshx:"+id
      -- 常开模式:secrets 表存 AES-GCM 密文(nonce+ciphertext),密钥由主密码 argon2id 派生
audit_log(id PK, ts, actor 'human'|'ai'|'mcp', action, detail JSON)  -- AI/MCP 执行的每条命令必记
settings(key PK, value JSON)
known_hosts(host, key_type, fingerprint, first_seen)  -- 同时尊重 ~/.ssh/known_hosts(只读合并)
port_forwards(id PK, node_id, type 'local'|'remote'|'dynamic', local_addr, remote_addr, autostart)
alerts_fired(id PK, node_id, metric, value, ts, notified_at)  -- 去重与静默窗口
share_links(id PK, session_id, token_hash, expires_at, revoked_at)  -- 只读链接,见 §4.13
```

> 已删除(v2 遗留):`users`、`shares(owner,invitee)`、`ai_conversations`、`ai_messages`。
> 前两者随团队场景一起删;后两者随内置 Agent 降级为可选,真做时再加。

**监控时序数据不入 SQLite**:每主机内存环形数组(1 小时 @5s = 720 点)。
每 5s × N 台写 SQLite 会持续磨损磁盘,而这些数据的价值窗口只有几小时。

### 4.4 凭据安全

- **桌面模式**:密码 / 私钥口令 → 系统钥匙串;SQLite 只存 `credential_ref`。
- **常开模式**:启动后首次访问输入主密码 → argon2id 派生 KEK → 解密 DEK(信封加密,方便改主密码)→ DEK 只在内存中解密凭据。

#### 明确决策:不做无人值守解锁

进程重启(重启机器、升级 sshx)后 vault 上锁,监控与告警暂停,直到你打开浏览器输一次主密码。

**不提供"自动解锁"**。要自动解锁就必须把密钥或主密码落盘,那等于回到 FinalShell 的错误
(硬编码密钥"加密" = 明文,见 finalshell-analysis.md §2)——而修掉这一点正是做这个产品的原始动机之一。

> 唯一诚实的替代方案是外部 KMS / TPM 托管密钥,个人场景不值得这个复杂度。

#### OpenSSH 兼容

- 导入向导:解析 `~/.ssh/config` 生成连接节点(Host/HostName/Port/User/IdentityFile/ProxyJump)——**这是首次启动向导的第一步**,决定新用户能否 60s 内用起来。
- 认证顺序:ssh-agent(若可用)→ 指定私钥文件 → vault 中密码。
- `ProxyJump` 多级跳板(x/crypto/ssh 原生嵌套连接)。
- known_hosts:读取合并 OpenSSH 的,新 host 弹指纹确认(TOFU),写入自己的表。
- 密钥管理:生成密钥对 + 一键部署公钥到主机(等价 `ssh-copy-id`)。

### 4.5 访问控制:用网络层代替权限系统

常开模式**默认不监听公网**。推荐部署:

```
sshx --remote --listen tailscale0   (或 127.0.0.1 + `tailscale serve`)
```

带来的删减与收益:

| v2 计划 | v3 |
|---|---|
| Let's Encrypt 自动证书 | **不需要**(`tailscale cert`,或 tailnet 内直接 HTTP) |
| 公网暴露 + 登录爆破防护 | 风险大幅下降(rate limit 仍保留作纵深防御) |
| 产品内的用户/权限系统 | **不做**——偶尔要给人看,就把对方拉进 tailnet |

理由:一个挂在公网的 Web 终端是极肥的目标,打穿一次 = 你所有服务器的凭据库。
在网络层挡住比在应用层写鉴权划算一个数量级。

主密码登录**仍然保留**(保护 vault + 纵深防御),但它不再是唯一防线。
若用户坚持要公网暴露,`--listen 0.0.0.0` 需显式加 `--i-know-what-im-doing`,并强制 HTTPS + rate limit。

### 4.6 免 agent 实时监控

> FinalShell 用户最离不开的功能。v2 曾把它放"后期",v3 放在 M6(AI 之前)。

- **采集方式:一条长驻 exec channel 跑循环**,而不是每 tick 一次往返:

  ```sh
  while :; do cat /proc/stat /proc/meminfo /proc/loadavg /proc/net/dev; \
    df -P; echo '---SSHX---'; sleep 5; done
  ```

  一个 channel、零per-tick往返、关闭 channel 即停止、纯文本本地解析。

- **不用 `top -bn1`**:输出格式在 busybox / BSD / 各版本间差异巨大,且开销不小。
- **能力探测**:连接建立后探一次 `/proc` 是否存在(macOS/BSD/网络设备没有或不同)。
  不支持则显示"监控不可用",**不要反复刷错误**。
- **监控对象是主机(node),不只是活跃会话**:可以对指定的重要主机保持一条**只做监控的轻量连接**,
  即使没开终端也在采集——这是"常开控制台"和"桌面客户端"的分界线。
- **配置**:每个连接可开关监控 + 采集间隔(默认 5s,下限 2s)。
- **展示**:终端面板侧边监控条(CPU/内存/负载/磁盘/网络)+ 近 1 小时折线图;总览页纵览所有受监控主机。
- **与 AI 联动**:`get_monitor_metrics` 作为 tool 暴露,"磁盘为什么满了"直接读指标而非解析终端输出。

### 4.7 告警(v3 新增,个人场景价值最高的一块)

监控从"我主动打开看"变成"它主动找我"——这是常开形态相对桌面应用的**结构性优势**,FinalShell 做不到。

- **规则**:每个 node 配 `alerts:[{metric, op, threshold, duration}]`,如 `disk > 90%`、`load1 > 8 持续 5min`、`host unreachable`。
- **通知渠道**(按实现成本排序):
  1. **Webhook / ntfy / Telegram Bot / 邮件** —— 先做这个,一个 HTTP POST 就完事;
  2. PWA Web Push —— 手机装了 PWA 后原生通知,M8 随 PWA 一起做。
- **去重与静默**:`alerts_fired` 记录已触发项,同一规则默认静默 30min,避免刷屏。
- **闭环**:通知点开 → 直达该主机的监控页 → 一键"问 AI"(Ctrl+K / MCP)→ 定位问题。
  这条链路是整个产品对个人用户最有说服力的场景。

### 4.8 AI:Tool Layer 是地基,三形态优先级反转

```
              ┌──────────── Tool Layer(唯一权威)────────────┐
              │ list_sessions / connect / close_session       │
              │ run_command(独立 exec channel)                │
              │ read_terminal(vt10x 渲染后文本)               │
              │ list_files / read_file / write_file / stat    │
              │ get_monitor_metrics                           │
              └──────┬──────────────────────┬────────────────┘
                     │                      │
              MCP Server(优先)        Ctrl+K 命令生成(优先)
              - 桌面: stdio            单次 LLM 调用,填入输入行
              - 常开: HTTP + token     不自动执行
                     │
              内置 Agent(最后,或不做)
```

#### 为什么反转优先级

| 形态 | 工作量 | 个人场景价值 | 排期 |
|---|---|---|---|
| **Ctrl+K 自然语言生成命令** | 很小 | **很高**——每天用几十次,忘了 `find` 参数按一下 | M7 |
| **MCP Server** | 小(工具层复用 + 官方 SDK) | **很高**——让**你已经在用的** Claude Code / 手机 AI 直接操作服务器 | M7 |
| **内置 Agent** | 大(provider 抽象、流式、两家 tool-use 差异、对话持久化、专门 UI) | **中**——你已经有更好的 agent 了 | M10,可选 |

核心判断:**你已经拥有一个比自己能造的更好的 Agent。**
在 sshx 里再写一个 agentic loop,是花几周做一个更差的 Claude Code;
而 MCP Server 用很小的代价,让那个更好的 agent 能操作你的机器。

内置 Agent 唯一的存在理由是**手机端**——希望在 sshx 的 PWA 里直接对话,而不是"打开 AI App → 它通过远程 MCP 连回 sshx"。
这条链路取决于手机 AI 客户端对远程 MCP 的支持程度。**先按 MCP 路线做,M8 真跑通一次手机场景再决定要不要补内置 Agent。**

#### Guard:三层,且明确它不是安全边界

正则黑名单在 LLM 面前是纸糊的(`rm -fr /`、`find -delete`、`truncate -s0`、`$(base64 -d)`、heredoc、
mysql shell 里的 `DROP DATABASE`……列不完),而且"危险"是**按主机而定**的,正则不知道哪台是生产库。

1. **结构性护栏(唯一真正的安全属性)**:连接节点上的 `aiPolicy`。
   - `readonly`:AI 只能跑只读白名单命令(`ls/cat/df/ps/journalctl/systemctl status/...`),**无论它说什么**。生产机默认此项。
   - `confirm`:任何写操作挂起等确认。
   - `trusted`:直接执行(测试机)。
2. **白名单优于黑名单**(AI 通道):默认"不在只读白名单里的都要确认"。白名单有界,黑名单无界。
3. **正则黑名单降级为"误操作提醒"**,同时作用于人类输入。**文档明说它不是安全机制**,不要让任何人依赖它。

> **堵住 v2 的洞**:`write_terminal`(往交互终端写任意键盘字节)会绕过整个 Guard。
> v3 **从 AI 工具集中删除 `write_terminal`**;MCP 侧如需保留,必须走与危险命令相同的确认流。

Guard 挂在 `tool.Registry.Call` 分发前,写一次,MCP 与(未来的)内置 Agent 都受益。全部进 `audit_log`。

#### AI 读屏策略(正视局限)

- 优先 `run_command`(独立 exec channel,干净 stdout),而非读交互屏;
- `read_terminal` 用 vt10x 维护服务端虚拟终端状态,dump 渲染后文本;
- 检测 alternate-screen(vim/top/htop)时拒绝读屏,并明确告知 AI "当前在 TUI 应用中";
- 维护"命令 + 输出"日志,比原始终端流干净得多。

#### 隐私提示

MCP over HTTP 意味着**第三方 AI 客户端的服务商会看到终端输出**(可能含密钥、日志、业务数据)。
设置项提供输出脱敏(常见密钥模式打码)+ 文档明确告知。

### 4.9 终端↔SFTP 联动(cwd 跟踪)

1. 首选 **OSC 7**:可选注入 shell integration(PROMPT_COMMAND/precmd 钩子,用户可关),
   每次提示符输出 `OSC 7;file://host/path`,服务端解析后推 `cwd` 事件,SFTP 面板跟随。
2. Fallback:面板点"同步到终端位置"时,通过 exec channel 跑 `pwd`(不污染终端屏幕)。
3. **远程文件编辑**:双击文件 → Monaco 编辑器打开 → 保存自动回传。
   (FinalShell 的"本地编辑器打开"在浏览器形态下做不到,内置编辑器是现实答案。)

### 4.10 端口转发 / 隧道

访问内网数据库 / 内部服务的刚需,个人场景高频。

- **本地转发**(`-L`)/ **远程转发**(`-R`)/ **动态转发**(`-D`,SOCKS)。
- **API**:`POST /api/forwards` → 状态写 `port_forwards`;WS 推送状态变更。
- **UI**:连接树右键"端口转发",活跃隧道列表 + 增删。
- **自动启动**:节点可配 `autostart`,连接建立即拉起常用隧道(个人用户往往每次都开同样几条)。
- **生命周期**:绑定 session,session 关闭时自动清理,避免端口泄漏。

### 4.11 多会话命令广播

- **实现**:选中多个 session → 命令输入框 → 回车,对每个 session 调 `Exec`。
- **权限**:过 Guard(危险命令同样提醒)。
- **结果聚合**:分标签页或合并视图(前缀标注来源)。
- 个人场景规模较小(几台机 `apt upgrade`),不追求 50 台并发的调度复杂度。

### 4.12 移动端定位

> 手机键盘 + 终端是灾难体验。手机不是用来"自己敲命令"的。

- **接收告警**:Web Push 通知,点开直达问题主机。
- **只读监控**:纵览所有受监控主机的指标。
- **AI 代办**:手机 AI 客户端通过 MCP 连 sshx 执行诊断 / 修复。
- **围观会话**:`sizeMode: passive` 只读附着,看笔记本上正在跑的输出,不改 PTY 尺寸。
- **危险操作确认**:AI 提出的高危操作推到手机,点确认才执行。
- **不做**:手机上的交互式终端键盘输入。

### 4.13 只读分享链接(团队功能的唯一残留)

全套权限系统删掉后,保留一个几十行的功能覆盖"临时喊人看一眼":

- `POST /api/sessions/{id}/share` → 生成含 token 的只读链接,**强制过期**(默认 30min)。
- 只读:服务端**丢弃**该连接的所有输入字节,`sizeMode` 强制 `passive`。
- 可随时吊销(`share_links.revoked_at`),UI 常驻显示"N 人正在观看"。
- ⚠ 只读也会泄漏屏幕内容(你敲进终端的密钥对方看得见),UI 上必须提示。
- tailnet 部署下链接对外网无效——这是特性不是缺陷;要给外人看就临时开 `tailscale funnel`。

---

## 五、项目目录结构

```
├── cmd/sshx/main.go              # CLI 入口(serve --local / --remote / mcp)
├── internal/
│   ├── server/                   # HTTP 路由、WS handler、embed 前端
│   ├── auth/                     # Authenticator 接口 + local/remote 实现
│   ├── session/                  # SessionManager、Session、Terminal、环形buffer、Exec
│   ├── sshclient/                # 拨号、ProxyJump、agent、known_hosts、SFTP
│   ├── connect/                  # 连接编排(节点 → 拨号 → 会话)
│   ├── vault/                    # Vault 接口 + keyring/encrypted 实现
│   ├── store/                    # SQLite、迁移、nodes/audit/forwards/alerts
│   ├── monitor/                  # 免 agent 采集器(长驻 exec + proc 解析)
│   ├── alert/                    # 规则求值、去重静默、通知渠道(webhook/push)
│   ├── forward/                  # 端口转发管理(local/remote/dynamic)
│   ├── tool/                     # ★ 工具层(MCP 与 AI 共享地基)+ guard
│   ├── mcp/                      # MCP server(stdio + HTTP),复用 tool layer
│   ├── llm/                      # 薄封装,供 Ctrl+K 单次调用
│   └── sshconfig/                # ~/.ssh/config 导入
├── web/                          # React + Vite 前端
│   └── src/
│       ├── terminal/             # xterm 封装、WS 协议客户端、scrollback 搜索
│       ├── tree/                 # 连接树
│       ├── sftp/                 # 文件面板 + Monaco 远程编辑
│       ├── monitor/              # 监控面板 + 主机总览
│       ├── forward/              # 端口转发管理面板
│       ├── ai/                   # Ctrl+K、确认弹窗
│       └── layout/               # 桌面/移动响应式布局
└── Makefile / Taskfile.yml       # build: 前端构建→embed→go build
```

> `desktop/`(Wails 壳)从结构中移除,降级为可选,视需要再加。

---

## 六、实施里程碑

> M1–M4 已交付并测试通过(见 git 历史)。

### 已完成

| # | 里程碑 | 状态 |
|---|---|---|
| M1 | 骨架+单会话终端 | ✅ Go 服务 + WS 协议 + xterm.js |
| M2 | 连接管理+凭据 | ✅ 连接树 CRUD、SQLite、keyring、known_hosts TOFU、ssh_config 导入、ProxyJump、agent/key/password |
| M3 | 持久会话 | ✅ 环形 buffer 回放、多端 fan-out、keepalive、idle TTL、WS 自动重连 |
| M4 | SFTP 面板 | ✅ list/upload/download/mkdir/delete/rename、OSC 7 cwd 跟踪、shell integration |

### 待实施

| # | 里程碑 | 内容 | 验证方式 |
|---|---|---|---|
| **M5** | **地基修补** | ✅ 尺寸协商(`sizeMode` + 权威 `size` 下发);`Backend.Exec` / `ExecStream`;`internal/tool/` 包 + Registry + Guard 三层(含 `aiPolicy`) | 单测:尺寸协商取最小值、passive 不改 PTY、Exec 返回码/超时、`aiPolicy:readonly` 下写操作被拒 |
| **M6** | **监控 + 告警** | 长驻 exec 采集、能力探测、内存时序、主机总览与折线图、告警规则、webhook/ntfy 通知 | 连上自动显示监控;`dd` 撑满磁盘 → 手机/Telegram 收到告警 |
| **M7** | **AI(轻形态)** | Ctrl+K 命令生成;MCP Server(stdio + HTTP + token);`get_monitor_metrics` 等工具;audit | Ctrl+K "找出最大的 10 个文件";Claude Code 经 MCP 诊断磁盘占用;readonly 主机上写操作被结构性拒绝 |
| **M8** | **常开模式(主形态)** | 单账号登录 + encrypted vault + 解锁流程;tailnet 绑定;PWA + Web Push;systemd 部署;移动端布局 | 部署到常开机器;笔记本合盖后手机 reattach 同一会话;重启后解锁流程正确 |
| **M9** | **日常手感补齐** | 端口转发(含 autostart)、命令广播、scrollback 搜索、Monaco 远程编辑、密钥生成/部署、首次启动向导、终端外观设置(字号已完成,余字体/主题/连字) | 首次启动 60s 内用起来;开本地转发访问远端数据库;搜半小时前的日志 |
| **M10** | **内置 Agent(可选)** | provider 抽象、agent loop、对话持久化、专用 UI | 仅当 M8 证明手机 MCP 链路不好用时才做 |

### 顺序逻辑

```
M5 是地基,M6/M7 都依赖 Exec 与 Guard
M6 兑现 MVP 承诺(监控是留存功能),且为 M7 提供 get_monitor_metrics
M7 用最小代价拿到最大 AI 价值,不碰内置 Agent
M8 让主形态成立 —— 常开 + 手机,前面所有功能到这里才真正闭环
M9 补齐日常手感,决定是否成为你每天打开的那个工具
M10 按需
```

排序原则:**确定性优先**。AI 是全项目最可能推倒重来的部分,把高确定性的监控/告警/常开模式排在前面,
让 M7 的 AI 落在一个工具已齐备的地基上,而不是一边造工具一边造 Agent。

M5–M7 全程只跑桌面模式(`--local`),降低前期复杂度;M8 切换主形态。

---

## 七、明确不做

写下来是为了防止范围蔓延:

| 不做 | 理由 |
|---|---|
| 多用户 / RBAC / 审批工作流 | 个人场景;要多租户请用 Teleport/JumpServer |
| 团队协作(共享可写会话、光标区分、操作可见性) | 同上;只保留只读分享链接 |
| zmodem(rz/sz) | SFTP 面板 + 拖拽上传已覆盖;协议实现成本高 |
| 本地终端(ConPTY / 本地 shell) | 核心诉求是远程;后期可用 `aymanbagabas/go-pty` 补 |
| RDP / VNC / 串口 | Backend 接口留了扩展位,但不在路线图内 |
| 云同步 / 私有云账号 | 配置人类可读 + 自己 git 管;不锁定 |
| 插件系统 | MCP 就是扩展接口,不再造一套 |
| 无人值守 vault 解锁 | 见 §4.4,会退化成 FinalShell 的错误 |

---

## 八、风险与对策

- **xterm.js 大流量卡顿**:服务端输出节流合帧(~60fps flush);WebGL 失败自动降级 canvas;指标见 §0。
- **持久会话内存**:环形 buffer 固定上限(1MB/终端);会话数上限 + TTL 回收;目标 10 会话 < 200MB。
- **sshd MaxSessions 耗尽**:监控用单条长驻 channel;Exec 失败降级并提示,不无声重试。
- **监控采集对远端负载**:采集间隔下限 2s;单条命令一次取全部指标;用户可关。
- **非 Linux 目标**:能力探测失败即显示"监控不可用",不刷错误。
- **常开模式安全面**:默认只监听 tailnet;公网暴露需显式 flag + 强制 HTTPS + rate limit;MCP 独立 token 可吊销;audit_log 全记录。
- **Guard 不是安全边界**:靠 `aiPolicy` 结构性护栏兜底;文档明确不得依赖正则。
- **AI 读屏准确性**:vt10x 虚拟终端 + alternate-screen 检测 + 优先 `run_command`。
- **第三方 AI 服务商看到终端输出**:输出脱敏选项 + 文档明确告知。
- **端口转发资源泄漏**:forward 生命周期绑定 session,session 关闭自动清理。
- **告警刷屏**:静默窗口 + `alerts_fired` 去重。
- **vault 上锁期间监控停摆**:UI 显著提示"vault locked,监控已暂停",不静默失效。

---

## 九、验证(整体)

- 每个里程碑的手动验证如上表;Go 侧对 session/vault/tool/guard/monitor/alert 写单元测试。
- **§0 的产品指标每个里程碑跑一次**,退化即视为该里程碑未完成。
- **端到端冒烟(桌面模式)**:`sshx serve --local` → 首次启动导入 `~/.ssh/config` → 建连接 → 终端交互 →
  SFTP 传文件 + Monaco 改配置 → 监控面板显示 → Ctrl+K 生成一条命令 → 关浏览器重开 reattach。
- **主形态验收(常开模式)**:常开机器上 systemd 跑 `--remote`,绑定 tailnet →
  笔记本建会话 → 合盖 → 手机 PWA reattach 看到同一屏幕 →
  人为撑满磁盘 → 手机收到告警 → 点开 → Claude Code 经 MCP 诊断 →
  在 `aiPolicy:readonly` 的主机上验证写操作被结构性拒绝 →
  重启常开机器 → 验证 vault 上锁提示与解锁流程。
