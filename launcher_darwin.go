package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func launchCodex(port int, _ string) error {
	candidates := []string{
		"/Applications/ChatGPT.app/Contents/MacOS/ChatGPT",
		"/Applications/Codex.app/Contents/MacOS/Codex",
		filepath.Join(os.Getenv("HOME"), "Applications/ChatGPT.app/Contents/MacOS/ChatGPT"),
		filepath.Join(os.Getenv("HOME"), "Applications/Codex.app/Contents/MacOS/Codex"),
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			command := exec.Command(path, "--remote-debugging-address=127.0.0.1", fmt.Sprintf("--remote-debugging-port=%d", port))
			if err := command.Start(); err != nil {
				return err
			}
			return command.Process.Release()
		}
	}
	return errors.New("未找到 Codex 桌面应用；请先从官方渠道安装")
}

func openBrowser(url string) error {
	return exec.Command("open", url).Run()
}
