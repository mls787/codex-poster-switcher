package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed ui.html ui.css ui.js
var ui embed.FS

type appServer struct {
	library *library
	dir     string
	token   string
	origin  string
	port    int
	message string
	mu      sync.Mutex
	applyMu sync.Mutex
	quit    chan struct{}
	quitOne sync.Once
	logger  *log.Logger
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func availablePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port, nil
}

func sendJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func sendError(w http.ResponseWriter, status int, err error) {
	sendJSON(w, status, map[string]string{"error": err.Error()})
}

func (a *appServer) requirePOST(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		sendError(w, http.StatusMethodNotAllowed, errors.New("只接受 POST 请求"))
		return false
	}
	if r.Header.Get("Origin") != a.origin || r.Header.Get("X-Poster-Token") != a.token {
		sendError(w, http.StatusForbidden, errors.New("请求来源无效"))
		return false
	}
	return true
}

func (a *appServer) currentPort() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.port
}

func (a *appServer) setConnection(port int, message string) {
	a.mu.Lock()
	a.port = port
	a.message = message
	a.mu.Unlock()
}

func (a *appServer) state(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendError(w, http.StatusMethodNotAllowed, errors.New("只接受 GET 请求"))
		return
	}
	a.mu.Lock()
	connected, message := a.port != 0, a.message
	a.mu.Unlock()
	sendJSON(w, http.StatusOK, map[string]any{
		"library": a.library.snapshot(), "connected": connected, "message": message,
	})
}

func (a *appServer) importTheme(w http.ResponseWriter, r *http.Request) {
	if !a.requirePOST(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxImageBytes+(1<<20))
	if err := r.ParseMultipartForm(maxImageBytes + (1 << 20)); err != nil {
		sendError(w, http.StatusBadRequest, errors.New("图片上传失败或超过 10 MB"))
		return
	}
	file, header, err := r.FormFile("image")
	if err != nil {
		sendError(w, http.StatusBadRequest, errors.New("请选择一张 PNG 或 JPEG 图片"))
		return
	}
	defer file.Close()
	name := r.FormValue("name")
	if strings.TrimSpace(name) == "" {
		name = strings.TrimSuffix(filepath.Base(header.Filename), filepath.Ext(header.Filename))
	}
	t, err := a.library.add(name, file)
	if err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	if err := a.library.setActive(t.ID); err != nil {
		sendError(w, http.StatusInternalServerError, err)
		return
	}
	port := a.currentPort()
	message := "海报已保存。完全退出 Codex 后，点击“启动 Codex 并应用”。"
	if port != 0 {
		if err := a.applyCurrent(port); err == nil {
			message = "海报已应用到当前 Codex 窗口。"
		} else {
			a.logger.Printf("apply after import: %v", err)
		}
	}
	sendJSON(w, http.StatusOK, map[string]any{"theme": t, "message": message})
}

func (a *appServer) setActive(w http.ResponseWriter, r *http.Request) {
	if !a.requirePOST(w, r) {
		return
	}
	var input struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&input); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	if err := a.library.setActive(input.ID); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	message := "已选中海报。完全退出 Codex 后，点击“启动 Codex 并应用”。"
	if port := a.currentPort(); port != 0 {
		if err := a.applyCurrent(port); err == nil {
			message = "海报已切换。"
		} else {
			a.logger.Printf("apply after selection: %v", err)
		}
	}
	sendJSON(w, http.StatusOK, map[string]string{"message": message})
}

func (a *appServer) applyCurrent(port int) error {
	a.applyMu.Lock()
	defer a.applyMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return applyToCodex(ctx, port, a.library)
}

func (a *appServer) launch(w http.ResponseWriter, r *http.Request) {
	if !a.requirePOST(w, r) {
		return
	}
	if a.library.snapshot().Active == "" {
		sendError(w, http.StatusBadRequest, errors.New("请先导入并选中一张海报"))
		return
	}
	if port := a.currentPort(); port != 0 {
		if err := a.applyCurrent(port); err == nil {
			sendJSON(w, http.StatusOK, map[string]string{"message": "已应用到当前 Codex 窗口。"})
			return
		}
	}
	port, err := availablePort()
	if err != nil {
		sendError(w, http.StatusInternalServerError, err)
		return
	}
	if err := launchCodex(port, filepath.Join(a.dir, "codex-profile")); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if err := a.applyCurrent(port); err == nil {
			a.setConnection(port, "已连接 Codex，海报会在窗口刷新时自动恢复。")
			sendJSON(w, http.StatusOK, map[string]string{"message": "Codex 已启动，海报已应用。"})
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	a.setConnection(0, "未发现调试窗口。请完全退出 Codex，再点击启动；本工具不会自动重启它。")
	sendError(w, http.StatusGatewayTimeout, errors.New("Codex 已尝试启动，但未发现可换海报的窗口。请完全退出 Codex 后重试"))
}

func (a *appServer) restore(w http.ResponseWriter, r *http.Request) {
	if !a.requirePOST(w, r) {
		return
	}
	if err := a.library.setActive(""); err != nil {
		sendError(w, http.StatusInternalServerError, err)
		return
	}
	if port := a.currentPort(); port != 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		err := restoreCodex(ctx, port)
		cancel()
		if err != nil {
			a.logger.Printf("restore: %v", err)
		}
	}
	sendJSON(w, http.StatusOK, map[string]string{
		"message": "已移除本工具的海报。请完全退出 Codex，并从官方入口重新打开，以关闭调试端口。",
	})
}

func (a *appServer) deleteTheme(w http.ResponseWriter, r *http.Request) {
	if !a.requirePOST(w, r) {
		return
	}
	var input struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&input); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	wasActive := a.library.snapshot().Active == input.ID
	if err := a.library.delete(input.ID); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	if wasActive && a.currentPort() != 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		_ = restoreCodex(ctx, a.currentPort())
		cancel()
	}
	sendJSON(w, http.StatusOK, map[string]string{"message": "海报已从本机图库删除。"})
}

func (a *appServer) image(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/image/")
	t, data, err := a.library.get(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", t.MIME)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(data)
}

func (a *appServer) stop(w http.ResponseWriter, r *http.Request) {
	if !a.requirePOST(w, r) {
		return
	}
	sendJSON(w, http.StatusOK, map[string]string{"message": "海报设置器已退出。"})
	a.quitOne.Do(func() { close(a.quit) })
}

func (a *appServer) refreshLoop() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.quit:
			return
		case <-ticker.C:
			port := a.currentPort()
			if port == 0 || a.library.snapshot().Active == "" {
				continue
			}
			if err := a.applyCurrent(port); err != nil {
				a.logger.Printf("refresh: %v", err)
			}
		}
	}
}

func (a *appServer) mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		b, err := ui.ReadFile("ui.html")
		if err != nil {
			http.Error(w, "UI missing", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		_, _ = io.WriteString(w, strings.ReplaceAll(string(b), "{{TOKEN}}", a.token))
	})
	for _, filename := range []string{"ui.css", "ui.js"} {
		name := filename
		mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
			b, err := ui.ReadFile(name)
			if err != nil {
				http.Error(w, "UI missing", 500)
				return
			}
			if strings.HasSuffix(name, ".css") {
				w.Header().Set("Content-Type", "text/css; charset=utf-8")
			} else {
				w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			}
			_, _ = w.Write(b)
		})
	}
	mux.HandleFunc("/api/state", a.state)
	mux.HandleFunc("/api/import", a.importTheme)
	mux.HandleFunc("/api/activate", a.setActive)
	mux.HandleFunc("/api/launch", a.launch)
	mux.HandleFunc("/api/restore", a.restore)
	mux.HandleFunc("/api/delete", a.deleteTheme)
	mux.HandleFunc("/api/quit", a.stop)
	mux.HandleFunc("/image/", a.image)
	return mux
}

func run() error {
	userDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(userDir, "CodexPosterSwitcher")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(dir, "log.txt"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	logger := log.New(logFile, "poster ", log.LstdFlags)
	library, err := newLibrary(dir)
	if err != nil {
		return err
	}
	token, err := randomToken()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	origin := "http://127.0.0.1:" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	a := &appServer{library: library, dir: dir, token: token, origin: origin, quit: make(chan struct{}), logger: logger,
		message: "导入海报后，完全退出 Codex，再从这里启动。"}
	server := &http.Server{Handler: a.mux(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Printf("web server: %v", err)
		}
	}()
	go a.refreshLoop()
	if os.Getenv("CODEX_POSTER_NO_BROWSER") == "1" {
		fmt.Println(origin)
	} else if err := openBrowser(origin); err != nil {
		logger.Printf("open browser: %v", err)
		fmt.Println("在浏览器打开", origin)
	}
	<-a.quit
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return server.Shutdown(ctx)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
