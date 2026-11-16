# sshx 快速开始

## Windows

1. 解压 `sshx-Windows-amd64.zip` 到常用目录，双击 `sshx.exe`。
2. 程序会打开默认浏览器；没有自动打开时，复制控制台中的工作台地址。
3. 添加主机，填写地址、端口、登录用户及认证方式。
4. 点击“测试连接”，核对主机指纹；成功后点“保存并连接”。

控制台窗口代表本地服务，使用期间请保持打开。关闭浏览器或标签不会立即结束 SSH；关闭控制台或退出 sshx 则会结束本进程的 SSH 会话。

本地凭据保存在 Windows Credential Manager，配置数据库在用户配置目录内的 `sshx` 文件夹；升级时不要删除该目录。私钥路径指向运行 sshx 的机器。

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

从 https://github.com/doux93/sshd/releases 下载版本包，并使用旁边的 `.sha256` 文件核对。升级前备份数据库；停止旧程序、替换可执行文件、重新启动即可。配置与凭据继续使用原位置，服务重启需要重新连接 SSH。本版本尚未接入远端 tmux 恢复。

Windows 发布包暂未做代码签名，系统可能显示未知发布者提示。
