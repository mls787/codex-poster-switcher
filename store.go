package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxImageBytes = 10 << 20

type theme struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MIME      string `json:"mime"`
	CreatedAt string `json:"createdAt"`
}

type diskState struct {
	Active string  `json:"active"`
	Themes []theme `json:"themes"`
}

type library struct {
	mu    sync.Mutex
	dir   string
	state diskState
}

func newLibrary(dir string) (*library, error) {
	if err := os.MkdirAll(filepath.Join(dir, "images"), 0700); err != nil {
		return nil, err
	}
	l := &library{dir: dir, state: diskState{Themes: []theme{}}}
	b, err := os.ReadFile(filepath.Join(dir, "themes.json"))
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &l.state); err != nil {
		return nil, fmt.Errorf("主题索引损坏，请先备份 %s: %w", filepath.Join(dir, "themes.json"), err)
	}
	if l.state.Themes == nil {
		l.state.Themes = []theme{}
	}
	return l, nil
}

func imageExtension(mime string) string {
	if mime == "image/png" {
		return ".png"
	}
	return ".jpg"
}

func imageInfo(b []byte) (string, error) {
	if len(b) == 0 || len(b) > maxImageBytes {
		return "", errors.New("图片须小于 10 MB")
	}
	mime := http.DetectContentType(b)
	if mime != "image/png" && mime != "image/jpeg" {
		return "", errors.New("只支持 PNG 或 JPEG 图片")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width < 320 || cfg.Height < 180 || int64(cfg.Width)*int64(cfg.Height) > 50_000_000 {
		return "", errors.New("图片像素无效或超出 5000 万像素")
	}
	return mime, nil
}

func (l *library) saveLocked() error {
	b, err := json.MarshalIndent(l.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(l.dir, "themes.json.tmp")
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(l.dir, "themes.json"))
}

func (l *library) add(name string, r io.Reader) (theme, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxImageBytes+1))
	if err != nil {
		return theme{}, err
	}
	mime, err := imageInfo(b)
	if err != nil {
		return theme{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "我的海报"
	}
	if len([]rune(name)) > 60 {
		return theme{}, errors.New("名称最多 60 个字")
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return theme{}, err
	}
	t := theme{ID: hex.EncodeToString(idBytes), Name: name, MIME: mime, CreatedAt: time.Now().Format(time.RFC3339)}
	path := filepath.Join(l.dir, "images", t.ID+imageExtension(t.MIME))
	if err := os.WriteFile(path, b, 0600); err != nil {
		return theme{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.state.Themes = append(l.state.Themes, t)
	if err := l.saveLocked(); err != nil {
		l.state.Themes = l.state.Themes[:len(l.state.Themes)-1]
		_ = os.Remove(path)
		return theme{}, err
	}
	return t, nil
}

func (l *library) snapshot() diskState {
	l.mu.Lock()
	defer l.mu.Unlock()
	copyState := l.state
	copyState.Themes = append([]theme{}, l.state.Themes...)
	return copyState
}

func (l *library) get(id string) (theme, []byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, t := range l.state.Themes {
		if t.ID == id {
			b, err := os.ReadFile(filepath.Join(l.dir, "images", t.ID+imageExtension(t.MIME)))
			return t, b, err
		}
	}
	return theme{}, nil, os.ErrNotExist
}

func (l *library) setActive(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if id != "" {
		found := false
		for _, t := range l.state.Themes {
			if t.ID == id {
				found = true
				break
			}
		}
		if !found {
			return os.ErrNotExist
		}
	}
	previous := l.state.Active
	l.state.Active = id
	if err := l.saveLocked(); err != nil {
		l.state.Active = previous
		return err
	}
	return nil
}

func (l *library) delete(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	index := -1
	var t theme
	for i, candidate := range l.state.Themes {
		if candidate.ID == id {
			index, t = i, candidate
			break
		}
	}
	if index < 0 {
		return os.ErrNotExist
	}
	previous := l.state
	l.state.Themes = append(append([]theme{}, l.state.Themes[:index]...), l.state.Themes[index+1:]...)
	if l.state.Active == id {
		l.state.Active = ""
	}
	if err := l.saveLocked(); err != nil {
		l.state = previous
		return err
	}
	return os.Remove(filepath.Join(l.dir, "images", t.ID+imageExtension(t.MIME)))
}
