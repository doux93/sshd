# sshx 0.3.2

- 修复 MCP 畸形请求的错误响应，始终使用合法 JSON；拒绝请求尾部额外内容，校验失败时不执行工具。
- AI 命令助手默认改为 Ctrl+Shift+K，Ctrl+K 保留给 Shell；在“AI 设置”可切回旧快捷键，立即生效、刷新后保留，终端不重连。
- 上传排队、传输和保存期间，刷新或关闭页面请求浏览器确认；退出登录在撤销凭据前确认，取消退出可继续上传。任务结束后解除离开提醒。
- 保留此前的 tmux 终端恢复、分屏、主题和 SFTP 安全写入。

下载 Windows/Linux amd64 ZIP，解压阅读 QUICKSTART.md；升级时保留原数据库和凭据，按说明核对 SHA-256。

上传队列仍保存在页面内存，不支持断点续传；强制结束浏览器、程序或系统关机时无法保证出现提醒。完整 VT 屏幕快照、Windows 托盘与代码签名、完整 MCP Streamable HTTP 和用户真机验收仍未完成。

[验证记录](https://github.com/doux93/sshd/blob/main/docs/VALIDATION.md) · [Windows → Ubuntu 验收](https://github.com/doux93/sshd/blob/main/docs/WINDOWS_LINUX_ACCEPTANCE.md)
