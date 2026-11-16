# sshx 0.3.0

- 新增可选的远端 tmux 终端保活与恢复。工作台重启或断线后，重新接入原 Shell 进程、目录和变量。
- 在主机高级设置开启，默认关闭；远端需预装 tmux，每台已保存主机一个保活终端。
- 会话库区分“断开并保留”“结束远端终端”“忘记恢复记录”，结束远端任务需单独确认。
- 原终端丢失或连接目标变化时明确报错，不自动新建替代。提供手动接入命令。
- 保留明暗主题、分屏、常用命令及可取消重试的上传队列。

下载 Windows/Linux amd64 ZIP，解压阅读 QUICKSTART.md。发布包附 SHA-256 校验文件。

恢复的是仍在运行的远端 tmux 进程，不支持远端主机重启后复活任务。保活终端不注入 Shell 目录跟随脚本；手机只读查看。真实手机推送和 macOS 本机验收仍待完成。

[终端恢复指南](https://github.com/doux93/sshd/blob/main/docs/TERMINAL_RECOVERY.md) · [验证记录](https://github.com/doux93/sshd/blob/main/docs/VALIDATION.md)
