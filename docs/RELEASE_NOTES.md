# sshx 0.3.1

- 修复 Ubuntu 22.04 / tmux 3.2a 的首条命令被终端设备响应污染的问题：历史回放不再重复发送设备属性查询，实时查询仍保留。
- Windows 原生客户端增加 Ubuntu 22.04、24.04 独立验收，覆盖密码/私钥、中文路径、SFTP、真实断线和工作台进程重启恢复。
- 保留 0.3.0 的 tmux 保活、明暗主题、分屏、常用命令和上传队列。

建议使用 Ubuntu 22.04 且开启 tmux 保活的用户升级。下载 Windows/Linux amd64 ZIP，解压阅读 QUICKSTART.md；发布包附 SHA-256 校验文件。

远端需预装 tmux；远端主机重启或任务退出后不能恢复。实测是 Windows Server + WSL2 Ubuntu，用户 Windows 桌面、VPN、跳板及真实网络仍需验证。

[Ubuntu 验收记录](https://github.com/doux93/sshd/blob/main/docs/WINDOWS_LINUX_ACCEPTANCE.md) · [验证记录](https://github.com/doux93/sshd/blob/main/docs/VALIDATION.md)
