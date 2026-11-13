package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type cdpTarget struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	URL          string `json:"url"`
	WebSocketURL string `json:"webSocketDebuggerUrl"`
}

var cdpHTTP = &http.Client{Timeout: 2 * time.Second}

func fetchAppTargets(port int) ([]cdpTarget, error) {
	if port < 1 || port > 65535 {
		return nil, errors.New("无效调试端口")
	}
	resp, err := cdpHTTP.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/json/list")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("调试接口返回 %d", resp.StatusCode)
	}
	var all []cdpTarget
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&all); err != nil {
		return nil, err
	}
	result := make([]cdpTarget, 0, len(all))
	for _, target := range all {
		u, err := url.Parse(target.URL)
		if err == nil && target.Type == "page" && u.Scheme == "app" && target.WebSocketURL != "" {
			result = append(result, target)
		}
	}
	if len(result) == 0 {
		return nil, errors.New("未找到 Codex 页面；请确认已从本工具启动桌面应用")
	}
	return result, nil
}

// Chrome may advertise a non-loopback hostname. Always connect to the loopback
// port selected by this process, while retaining only the target path.
func safeWebSocketURL(port int, advertised string) (string, error) {
	u, err := url.Parse(advertised)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || !strings.HasPrefix(u.Path, "/devtools/page/") {
		return "", errors.New("无效的 Codex 调试页面地址")
	}
	u.Scheme = "ws"
	u.Host = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func evaluate(ctx context.Context, port int, target cdpTarget, expression string) (json.RawMessage, error) {
	wsURL, err := safeWebSocketURL(port, target.WebSocketURL)
	if err != nil {
		return nil, err
	}
	dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second}
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	request := map[string]any{
		"id": 1, "method": "Runtime.evaluate",
		"params": map[string]any{"expression": expression, "returnByValue": true, "awaitPromise": false},
	}
	if err := conn.WriteJSON(request); err != nil {
		return nil, err
	}
	for {
		var response struct {
			ID     int             `json:"id"`
			Error  json.RawMessage `json:"error"`
			Result struct {
				Result struct {
					Value json.RawMessage `json:"value"`
				} `json:"result"`
				Exception json.RawMessage `json:"exceptionDetails"`
			} `json:"result"`
		}
		if err := conn.ReadJSON(&response); err != nil {
			return nil, err
		}
		if response.ID != 1 {
			continue
		}
		if len(response.Error) > 0 || len(response.Result.Exception) > 0 {
			return nil, fmt.Errorf("Codex 拒绝样式更新: %s %s", response.Error, response.Result.Exception)
		}
		return response.Result.Result.Value, nil
	}
}

func javascriptString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func checkExpression(id string) string {
	return "(() => { const p = globalThis.__codexPoster; if (p && p.id === " + javascriptString(id) + " && document.getElementById('codex-poster-style') && document.getElementById('codex-poster-background')) { p.refresh(); return true; } return false; })()"
}

func applyExpression(id, mime string, image []byte) string {
	imageURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(image)
	return "(() => {\n" +
		"const id = " + javascriptString(id) + ";\n" +
		"const imageURL = " + javascriptString(imageURL) + ";\n" +
		`if (!document.body) return false;
if (globalThis.__codexPoster) globalThis.__codexPoster.stop();
const background = document.createElement('div');
background.id = 'codex-poster-background';
background.style.backgroundImage = 'linear-gradient(var(--poster-tint), var(--poster-tint)), url(' + JSON.stringify(imageURL) + ')';
document.body.prepend(background);
const style = document.createElement('style');
style.id = 'codex-poster-style';
style.textContent = ` + "`" + `
  :root { --poster-tint: rgba(255,255,255,.18); }
  @media (prefers-color-scheme: dark) { :root { --poster-tint: rgba(0,0,0,.28); } }
  html, body { background-color: transparent !important; }
  #codex-poster-background {
    position: fixed; inset: 0; z-index: 0; pointer-events: none;
    background-size: cover; background-position: center; background-repeat: no-repeat;
  }
  body > [data-codex-poster-root="1"] { position: relative; z-index: 1; background-color: transparent !important; }
  [data-codex-poster-surface="1"] { background-color: transparent !important; background-image: none !important; }
` + "`" + `;
document.head.append(style);
let disposed = false;
const refresh = () => {
  if (disposed) return;
  const w = window.innerWidth, h = window.innerHeight;
  for (const child of document.body.children) {
    if (child === background || child === style || child.tagName === 'SCRIPT') continue;
    const bounds = child.getBoundingClientRect();
    if (bounds.width >= w * .7 && bounds.height >= h * .7) {
      child.setAttribute('data-codex-poster-root', '1');
    }
  }
  const elements = document.querySelectorAll('body *');
  for (const element of elements) {
    if (element === background || element === style || element.closest('#codex-poster-background, [role="dialog"], [role="menu"], [data-state="open"][data-radix-menu-content]')) continue;
    const rect = element.getBoundingClientRect();
    if (rect.width < Math.max(220, w * .28) || rect.height < h * .30 || rect.width * rect.height < w * h * .18) continue;
    const computed = getComputedStyle(element);
    const color = computed.backgroundColor;
    if (color !== 'transparent' && color !== 'rgba(0, 0, 0, 0)') {
      element.setAttribute('data-codex-poster-surface', '1');
    }
  }
};
const observer = new MutationObserver(() => requestAnimationFrame(refresh));
observer.observe(document.body, { childList: true, subtree: true });
refresh();
globalThis.__codexPoster = { id, refresh, stop() {
  disposed = true;
  observer.disconnect();
  background.remove();
  style.remove();
  document.querySelectorAll('[data-codex-poster-root], [data-codex-poster-surface]').forEach(element => {
    element.removeAttribute('data-codex-poster-root');
    element.removeAttribute('data-codex-poster-surface');
  });
  delete globalThis.__codexPoster;
}};
return true;
})()`
}

func restoreExpression() string {
	return "(() => { if (globalThis.__codexPoster) { globalThis.__codexPoster.stop(); return true; } return false; })()"
}

func applyToCodex(ctx context.Context, port int, l *library) error {
	state := l.snapshot()
	if state.Active == "" {
		return errors.New("请先选择一张海报")
	}
	t, image, err := l.get(state.Active)
	if err != nil {
		return err
	}
	targets, err := fetchAppTargets(port)
	if err != nil {
		return err
	}
	var firstError error
	success := 0
	for _, target := range targets {
		current, err := evaluate(ctx, port, target, checkExpression(t.ID))
		if err == nil && string(current) != "true" {
			_, err = evaluate(ctx, port, target, applyExpression(t.ID, t.MIME, image))
		}
		if err != nil && firstError == nil {
			firstError = err
		} else if err == nil {
			success++
		}
	}
	if success == 0 {
		return firstError
	}
	return nil
}

func restoreCodex(ctx context.Context, port int) error {
	targets, err := fetchAppTargets(port)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if _, err := evaluate(ctx, port, target, restoreExpression()); err != nil {
			return err
		}
	}
	return nil
}
