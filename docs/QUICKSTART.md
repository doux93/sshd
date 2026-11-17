# sshx 快速开始

## Windows

1. 解压 `sshx-Windows-amd64.zip` 到常用目录，双击 `sshx.exe`。
2. 程序会打开默认浏览器；没有自动打开时，复制控制台中的工作台地址。
3. 添加主机，填写地址、端口、登录用户及认证方式。
4. 点击“测试连接”，核对主机指纹；成功后点“保存并连接”。

控制台窗口代表本地服务，使用期间请保持打开。关闭浏览器或标签不会立即结束 SSH；关闭控制台或退出 sshx 则会关闭 SSH 连接。普通终端随之结束；开启 tmux 保活的远端终端可从会话库重新接入。

本地凭据保存在 Windows Credential Manager，配置数据库在用户配置目录内的 `sshx` 文件夹；升级时不要删除该目录。私钥路径指向运行 sshx 的机器。

## 常用操作

- 在终端工具栏点击“搜索”，或按 Ctrl+Shift+F，查找当前终端屏幕及回滚内容（回滚上限 10,000 行）。Enter / Shift+Enter 切换匹配，Esc 关闭并回到终端；Ctrl+F 继续留给 Shell。
- 文件面板支持当前目录筛选；单击目录进入，单击文件下载，键盘 Tab / Enter 也可操作。地址栏输入路径后按 Enter，失败时保留原目录并显示错误。
- 命令助手可从终端工具栏直接打开，默认使用 Ctrl+Shift+K，Ctrl+K 留给 Shell。可在“AI 设置”切换快捷键，立即生效并记住当前浏览器的选择。
- 弹窗内 Tab 切换输入与按钮，Esc 取消并回到打开入口；保存或确认操作进行中不能关闭。
- 上传期间刷新或关闭网页会请求浏览器确认；取消离开继续上传。继续离开仍会中断任务并丢失队列，不支持断点续传。退出登录会先确认未完成上传。强制结束浏览器或系统关机无法保证出现提醒。

## Linux

解压 `sshx-Linux-amd64.zip`，运行 `chmod +x sshx && ./sshx`，打开控制台打印的地址。桌面模式需要可用的系统钥匙串；无图形界面的服务器请使用下方常开模式。

## 常开模式与手机

首次启动前创建只允许服务用户读取的主密码文件，内容为至少 8 字节的主密码。例如：

```sh
umask 077
read -r -s -p 'Master password: ' SSHX_PASSWORD
printf '%s' "$SSHX_PASSWORD" > master-password
unset SSHX_PASSWORD
./sshx --mode remote --addr 127.0.0.1:8722 --master-password-file ./master-password
```

首次初始化成功后程序会删除密码文件。通过 HTTPS 反向代理访问工作台并登录；手机打开同一 HTTPS 地址即可查看会话、监控和告警。在告警页主动开启当前设备通知。手机是只读查看，不能输入命令。部署详情见仓库 `docs/DEPLOYMENT.md`。

仅在本机 HTTP 测试远程模式时添加 `--secure-cookies=false`。

## 版本与升级

`sshx --version` 显示版本和源码提交。`--open-browser=false` 可关闭自动打开浏览器；Linux 桌面可传 `--open-browser` 显式打开。

从 https://github.com/doux93/sshd/releases 下载版本包，并使用旁边的 `.sha256` 文件核对。升级前备份数据库；停止旧程序、替换可执行文件、重新启动即可。配置与凭据继续使用原位置，服务重启需要重新连接 SSH。需要保留远端任务时，提前在主机高级设置开启“终端保活与恢复（tmux）”，远端需预装 tmux。工作台重启后在会话库点“恢复终端”；远端主机重启后不能恢复。请保留原数据库以保存恢复入口。

Windows 发布包暂未做代码签名，系统可能显示未知发布者提示。
