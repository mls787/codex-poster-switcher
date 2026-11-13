package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type startApp struct {
	Name  string `json:"Name"`
	AppID string `json:"AppID"`
}

func findCodexAppID() (string, error) {
	if override := os.Getenv("CODEX_POSTER_AUMID"); override != "" {
		return override, nil
	}
	// Get-StartApps lists the registered app identifier without reading or
	// modifying the protected WindowsApps installation directory.
	query := `Get-StartApps | Where-Object { $_.Name -match 'Codex|ChatGPT' } | Select-Object Name,AppID | ConvertTo-Json -Compress`
	output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", query).Output()
	if err != nil {
		return "", fmt.Errorf("读取 Windows 开始菜单失败: %w", err)
	}
	data := strings.TrimSpace(string(output))
	if data == "" || data == "null" {
		return "", errors.New("开始菜单里未找到 Codex；请先从官方渠道安装")
	}
	var apps []startApp
	if strings.HasPrefix(data, "[") {
		if err := json.Unmarshal(output, &apps); err != nil {
			return "", err
		}
	} else {
		var one startApp
		if err := json.Unmarshal(output, &one); err != nil {
			return "", err
		}
		apps = []startApp{one}
	}
	for _, app := range apps {
		if strings.EqualFold(app.Name, "Codex") && app.AppID != "" {
			return app.AppID, nil
		}
	}
	for _, app := range apps {
		if strings.Contains(strings.ToLower(app.Name), "codex") && app.AppID != "" {
			return app.AppID, nil
		}
	}
	return "", errors.New("开始菜单里未找到 Codex；当前 Windows 版本只支持注册为 Codex 的桌面应用")
}

func launchCodex(port int, profileDir string) error {
	id, err := findCodexAppID()
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	helper := filepath.Join(filepath.Dir(executable), "codex-activate.exe")
	if _, err := os.Stat(helper); err != nil {
		return errors.New("安装包缺少 codex-activate.exe，请重新下载完整 ZIP")
	}
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		return err
	}
	flags := fmt.Sprintf(`--remote-debugging-address=127.0.0.1 --remote-debugging-port=%d --user-data-dir="%s"`, port, profileDir)
	output, err := exec.Command(helper, id, flags).CombinedOutput()
	if err != nil {
		return fmt.Errorf("启动 Codex 失败: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

func openBrowser(url string) error {
	return exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url).Start()
}
