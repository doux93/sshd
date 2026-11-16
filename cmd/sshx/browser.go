package main

import (
	"errors"
	"net"
	"net/url"
	"os/exec"
	"runtime"
)

// Pass the loopback URL as an argument, never through a command shell.
func launchBrowser(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		return errors.New("browser launcher requires a local workbench URL")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", raw)
	case "darwin":
		cmd = exec.Command("open", raw)
	default:
		cmd = exec.Command("xdg-open", raw)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
