package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestSafeWebSocketURL(t *testing.T) {
	got, err := safeWebSocketURL(12345, "ws://untrusted.example:9999/devtools/page/abc?secret=1")
	if err != nil || got != "ws://127.0.0.1:12345/devtools/page/abc" {
		t.Fatalf("unsafe target URL: %q, %v", got, err)
	}
	if _, err := safeWebSocketURL(12345, "ws://untrusted.example/devtools/browser/abc"); err == nil {
		t.Fatal("browser websocket was accepted")
	}
}

func TestInjectedJavaScriptParses(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node.js is not installed")
	}
	command := exec.Command("node", "--check", "-")
	command.Stdin = strings.NewReader(applyExpression("theme-id", "image/png", []byte("sample")))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("injected JavaScript does not parse: %s: %v", output, err)
	}
}

func TestCDPFiltersOtherPagesAndApplies(t *testing.T) {
	library, err := newLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	theme, err := library.add("test", strings.NewReader(string(testPNG(t))))
	if err != nil {
		t.Fatal(err)
	}
	if err := library.setActive(theme.ID); err != nil {
		t.Fatal(err)
	}
	var expressions []string
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	var port int
	mux := http.NewServeMux()
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]cdpTarget{
			{Type: "page", URL: "https://example.com/", WebSocketURL: fmt.Sprintf("ws://127.0.0.1:%d/devtools/page/other", port)},
			{Type: "page", URL: "app://codex/home", WebSocketURL: fmt.Sprintf("ws://127.0.0.1:%d/devtools/page/codex", port)},
		})
	})
	mux.HandleFunc("/devtools/page/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/devtools/page/codex" {
			http.Error(w, "wrong target", 400)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var request struct {
			ID     int `json:"id"`
			Params struct {
				Expression string `json:"expression"`
			} `json:"params"`
		}
		if err := conn.ReadJSON(&request); err != nil {
			return
		}
		expressions = append(expressions, request.Params.Expression)
		value := false
		if strings.Contains(request.Params.Expression, "document.body") {
			value = true
		}
		_ = conn.WriteJSON(map[string]any{"id": request.ID, "result": map[string]any{"result": map[string]any{"value": value}}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	u, _ := url.Parse(server.URL)
	port, _ = strconv.Atoi(u.Port())
	if err := applyToCodex(context.Background(), port, library); err != nil {
		t.Fatal(err)
	}
	if len(expressions) != 2 || !strings.Contains(expressions[1], "codex-poster-background") {
		t.Fatalf("unexpected CDP expressions: %d", len(expressions))
	}
}
