# Windows 客户端连接 Linux 的发布包验收

主要使用场景是 Windows 本地运行 `sshx.exe`，通过浏览器操作 Linux SSH 服务器。

工作流 [Windows to Linux acceptance](https://github.com/doux93/sshd/actions/workflows/windows-linux.yml) 手动运行时下载指定 Release；自动运行时使用 Verify 工作流成功后生成的同一份 Windows ZIP。两种模式都核对 SHA-256 和 VERSION，再启动包内原生 Windows 可执行文件，不在验收机上重新编译。结果记录包来源和 SHA-256，开发包不冒充已发布版本。

2026-11-16 的 [v0.3.1 正式包验收](https://github.com/doux93/sshd/actions/runs/43863924570)中，两个 Ubuntu 版本分别通过全部 10 个阶段。

| 远端系统 | OpenSSH 服务端 | tmux | 结果 | 证据 |
|---|---|---|---|---|
| Ubuntu 22.04 | `1:8.9p1-3ubuntu0.18` | tmux 3.2a | 10/10 通过 | [JSON](evidence/windows-ubuntu-22.04-v0.3.1.json) · [截图](images/windows-ubuntu-22.04-v0.3.1.png) |
| Ubuntu 24.04 | `1:9.6p1-3ubuntu13.20` | tmux 3.4 | 10/10 通过 | [JSON](evidence/windows-ubuntu-24.04-v0.3.1.json) · [截图](images/windows-ubuntu-24.04-v0.3.1.png) |

两个结果记录的 Windows 包 SHA-256 均为 `f2efb82249ff5da1b148f65fa01c392054d41845c84828ddb931979c6a003f39`，与 [v0.3.1 Release](https://github.com/doux93/sshd/releases/tag/v0.3.1) 的下载包一致。

此次补测发现并修复了 v0.3.0 在 Ubuntu 22.04 / tmux 3.2a 下的问题：历史回放再次触发设备属性查询，迟到的响应进入 Shell，干扰首条命令。修复只过滤历史查询，实时查询与显示控制保留。[旧版失败证据](evidence/windows-ubuntu-22.04-v0.3.0-failure.json)已保留；此前 Ubuntu 24.04 的通过结果只代表该次环境，见[历史结果](evidence/windows-linux-v0.3.0.json)。

## 环境与范围

- 客户端：GitHub 托管 Windows Server 2025，原生 `sshx.exe`、Windows Credential Manager 和 Chromium。
- 服务器：分别在独立测试机内安装 WSL2 Ubuntu 22.04、24.04，运行真实 OpenSSH、internal-sftp 和 tmux。脚本读取 `/etc/os-release` 校验实际发行版，并记录 OpenSSH/tmux 版本。
- 数据：随机临时 Linux 用户、密码、密钥、独立数据库和测试文件，退出时清理；不使用用户服务器或凭据。
- 网络中断：关闭仅转发本次 SSH 流量的 TCP 连接，重新连接后检查原进程；不修改宿主机网络配置。

这是受控环境下的跨系统实测，不能替代用户 Windows 10/11 桌面、实际网络、VPN、跳板机、企业安全软件或具体 Linux 发行版的验收。

## 检查内容

1. 被测包内程序从含中文及空格的 Windows 路径启动；未认证 API 被拒绝。
2. 真实 SSH 主机指纹确认，错误密码拒绝，草稿测试不保存主机或创建终端。
3. 密码经 Windows 凭据库保存后连接 Linux；私钥位于 Windows 中文及空格路径时也能登录。
4. 浏览器键盘输入进入真实 Linux Shell。
5. 浏览器上传 2 MiB 随机二进制文件，中文及空格文件名正确；Linux 文件校验、下载内容校验和重命名一致。
6. SSH TCP 连接中断后，恢复同一 tmux Shell、后台进程、环境变量与目录。
7. 强制结束 Windows 工作台进程，重新启动同一数据库，再读取 Windows 保存的密码恢复原终端；旧工作台 Token 失效。
8. 浏览器能找回恢复后的终端，并保存实际界面截图。
9. 结束远端终端需要显式确认，确认后实际 tmux 会话消失；删除测试主机与文件。

两个 Ubuntu 版本分别运行，不因其中一个失败而跳过另一个。结果见各自 `windows-linux-acceptance-Ubuntu-22.04` / `windows-linux-acceptance-Ubuntu-24.04` 附件中的 `results.json` 和 `windows-linux-recovery.png`。只有 `success: true` 才代表完整检查通过；失败时保留此前通过的项目及失败原因。实际运行结论另记入 [验证记录](VALIDATION.md)。

## 复现

在 Actions 中选择上述工作流，点击 Run workflow，填写已发布标签（默认 `v0.3.1`）。脚本仅允许在临时 GitHub Windows runner 中执行，避免修改个人 WSL 环境。它会创建临时 Linux 用户并启动隔离 SSH 服务。

你实际使用的服务器还需要验证：常用登录方式、真实文件权限、代理/跳板、网络断开方式及日常任务。这个工作流不会自动连接这些环境。
