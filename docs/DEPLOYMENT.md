# 部署 sshx

## 构建与首次启动

先按 README 构建前端，再构建 Go 二进制。不要直接执行原文档中的 `go install ./cmd/sshx@latest`：这里应从源码构建。

Linux 示例：

```sh
sudo useradd --system --home /var/lib/sshx --shell /usr/sbin/nologin sshx
sudo install -d -o sshx -g sshx -m 0700 /var/lib/sshx
sudo install -o sshx -g sshx -m 0600 /dev/null /var/lib/sshx/master-password
sudoedit /var/lib/sshx/master-password
sudo install -m 0755 ./sshx /usr/local/bin/sshx
sudo cp contrib/systemd/sshx.service /etc/systemd/system/sshx.service
sudo systemctl daemon-reload
sudo systemctl enable --now sshx
sudo journalctl -u sshx -f
```

主密码文件必须由服务用户可读，其目录必须允许服务用户删除文件。初始化成功后文件会被删除；初始化失败时保留。若删除失败，日志会提示人工删除。这里删除文件不承诺对存储介质进行安全擦除。

首次启动仍要求密码文件；后续启动复用已有认证配置，不要求文件存在。密码须至少 8 字节，最多 1024 字节。启动后通过 `/login` 登录；服务重启后需重新登录解锁内存中的凭据密钥。

## HTTPS 反向代理

服务默认监听回环地址。Caddy 示例：

```caddyfile
sshx.example.com {
    reverse_proxy 127.0.0.1:8722
}
```

Nginx 应保留浏览器访问的 Host，并转发 WebSocket Upgrade：

```nginx
location / {
    proxy_pass http://127.0.0.1:8722;
    proxy_http_version 1.1;
    proxy_set_header Host $http_host;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_read_timeout 90s;
}
```

以上 Nginx location 需置于已配置 TLS 的 server 内。

远程模式默认 `--secure-cookies=true`，适用于代理终止 TLS 的场景；不根据未经验证的 `X-Forwarded-Proto` 自动改变 Cookie 属性。仅做回环 HTTP 测试时显式加 `--secure-cookies=false`。

应用验证浏览器请求的 Origin/Host。代理若把 Host 改成后端地址，浏览器的写请求或 WebSocket 会被拒绝。登录限制按直接对端地址计数，因此默认反向代理后的所有设备共享登录限额：每分钟最多 10 次，最多同时进行 2 次密码验证。

也可配置 `--tls-cert` 和 `--tls-key` 直接提供 HTTPS。两者须同时提供。示例 systemd 服务以非 root 用户运行，不会自动获得绑定 443 的能力；可使用 8443，或通过前置代理提供 443。

## 备份、升级与会话

备份 SQLite 数据库和主密码，二者需分别妥善保存。密码无法找回；缺少密码无法解密凭据。主机名称、地址和登录会话等元数据不是加密存储。

```sh
sudo systemctl stop sshx
sudo sqlite3 /var/lib/sshx/sshx.db ".backup '/backup/sshx.db'"
sudo systemctl start sshx
```

升级前备份，替换二进制后重启。迁移将 schema 和版本号放在同一个事务中提交；遇到迁移错误不会留下该次迁移的半成品。

浏览器关闭后，服务端 SSH 会话仍可继续运行至空闲回收。**服务重启会终止 SSH 会话**；SQLite 中的 `sessions` 表保存的是网页登录凭据，不是 SSH 终端。需要远端任务跨服务重启存活时，应使用远端 tmux 等独立任务管理机制。

## 变更后的使用方式

- 退出登录撤销该登录凭据，并关闭使用它的 WebSocket；其他设备的登录不受影响。
- 重启后有效的旧 Cookie 仍不能解锁凭据库，页面会要求重新登录。
- Ctrl+K 提供命令预览及复制，不替用户输入终端。
- 默认 AI 策略只允许固定只读工具。需执行任意 Shell 的节点应明确设为 trusted。
- SFTP 删除或重命名符号链接只操作链接；递归删除不主动进入符号链接目录。SFTP 无法完全消除远端其他进程并发替换路径带来的竞态。
- 上传只覆盖普通文件，拒绝覆盖符号链接。临时文件在传输失败时清理；原子覆盖不可用时保留原文件并报错。

## 本次更新

启动会自动执行 schema v5 迁移，仅增加告警历史表。仍建议升级前停服备份。告警历史保留最近 1,000 条，确认状态对这个工作台的所有设备一致；确认表示已查看，不代表问题已经修复。

新版本主机表单可配置告警规则，保存后需重新连接该主机，新的监控参数与规则才会生效。关闭视图不触发 SSH 断开；主动结束会话请从“会话库”操作。无附着会话仍按 session TTL 回收。

通知在告警页主动启用，登录流程不再请求通知权限。AI 返回结构化 JSON 的命令说明；自定义模型需要遵守提示中的输出格式。

## 验证限制

本修订在 Linux 上执行了 Go 竞态测试、真实 OpenSSH SFTP 集成测试、前端协议测试与生产构建。完整浏览器 E2E、Windows 系统钥匙串、真实移动设备推送和长时间网络故障压测仍需在目标环境完成。
