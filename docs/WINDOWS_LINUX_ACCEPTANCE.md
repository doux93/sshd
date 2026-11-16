# Windows 客户端连接 Linux 的发布包验收

主要使用场景是 Windows 本地运行 `sshx.exe`，通过浏览器操作 Linux SSH 服务器。

工作流 [Windows to Linux acceptance](https://github.com/doux93/sshd/actions/workflows/windows-linux.yml) 下载指定的已发布 Windows ZIP，核对 SHA-256 和 VERSION，再启动其中的原生 Windows 可执行文件。它不以重新编译的开发程序代替发布包。

2026-11-16 已完成 [v0.3.0 实测](https://github.com/doux93/sshd/actions/runs/43858021729)，10 个阶段全部通过。发布程序未发现需要修复的问题；详细证据见 [结果 JSON](evidence/windows-linux-v0.3.0.json) 和 [界面截图](images/windows-linux-recovery.png)。

## 环境与范围

- 客户端：GitHub 托管 Windows Server 2025，原生 `sshx.exe`、Windows Credential Manager 和 Chromium。
- 服务器：同一验收虚拟机内的 WSL2 Ubuntu 24.04，运行真实 OpenSSH、internal-sftp 和 tmux。
- 数据：随机临时 Linux 用户、密码、密钥、独立数据库和测试文件，退出时清理；不使用用户服务器或凭据。
- 网络中断：关闭仅转发本次 SSH 流量的 TCP 连接，重新连接后检查原进程；不修改宿主机网络配置。

这是受控环境下的跨系统实测，不能替代用户 Windows 10/11 桌面、实际网络、VPN、跳板机、企业安全软件或具体 Linux 发行版的验收。

## 检查内容

1. 已发布程序从含中文及空格的 Windows 路径启动；未认证 API 被拒绝。
2. 真实 SSH 主机指纹确认，错误密码拒绝，草稿测试不保存主机或创建终端。
3. 密码经 Windows 凭据库保存后连接 Linux；私钥位于 Windows 中文及空格路径时也能登录。
4. 浏览器键盘输入进入真实 Linux Shell。
5. 浏览器上传 2 MiB 随机二进制文件，中文及空格文件名正确；Linux 文件校验、下载内容校验和重命名一致。
6. SSH TCP 连接中断后，恢复同一 tmux Shell、后台进程、环境变量与目录。
7. 强制结束 Windows 工作台进程，重新启动同一数据库，再读取 Windows 保存的密码恢复原终端；旧工作台 Token 失效。
8. 浏览器能找回恢复后的终端，并保存实际界面截图。
9. 结束远端终端需要显式确认，确认后实际 tmux 会话消失；删除测试主机与文件。

结果见工作流的 `results.json` 和 `windows-linux-recovery.png` 附件。只有 `success: true` 才代表完整检查通过；失败时保留此前通过的项目及失败原因。实际运行结论另记入 [验证记录](VALIDATION.md)。

## 复现

在 Actions 中选择上述工作流，点击 Run workflow，填写已发布标签（默认 `v0.3.0`）。脚本仅允许在临时 GitHub Windows runner 中执行，避免修改个人 WSL 环境。它会创建临时 Linux 用户并启动隔离 SSH 服务。

你实际使用的服务器还需要验证：常用登录方式、真实文件权限、代理/跳板、网络断开方式及日常任务。这个工作流不会自动连接这些环境。
