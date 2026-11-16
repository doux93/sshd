package server

import (
	"errors"
	"net"
	"os"
	"strings"
	"syscall"
)

func dialErrorHint(err error) (string, string) {
	var dns *net.DNSError
	var network net.Error
	switch {
	case errors.As(err, &dns):
		return "dns", "无法解析主机地址，请检查域名、DNS 或 VPN 连接。"
	case errors.As(err, &network) && network.Timeout():
		return "timeout", "连接超时，请检查地址、端口、防火墙，以及代理或跳板机是否可达。"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused", "目标拒绝连接，请确认 SSH 服务已启动，端口填写正确。"
	case os.IsNotExist(err):
		return "key_file", "找不到私钥文件，请检查运行 sshx 的机器上的文件路径。"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "unable to authenticate"), strings.Contains(message, "no supported methods remain"):
		return "authentication", "认证失败，请检查登录用户、密码或私钥，并确认服务器允许此认证方式。"
	case strings.Contains(message, "vault is locked"), strings.Contains(message, "vault locked"):
		return "vault_locked", "凭据库尚未解锁，请重新登录工作台后再连接。"
	case strings.Contains(message, "agent"):
		return "agent", "请确认运行 sshx 的机器上已启动 SSH Agent，并加载可用私钥。"
	default:
		return "connection", "连接未完成，请检查下方错误详情及主机、代理和跳板配置。"
	}
}
