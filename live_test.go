package main

import (
	"bytes"
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

// Optional local check against an already-open Codex debug port. It never
// launches or restarts Codex and always removes its own temporary style.
func TestLivePoster(t *testing.T) {
	portText := os.Getenv("CODEX_POSTER_LIVE_PORT")
	imagePath := os.Getenv("CODEX_POSTER_LIVE_IMAGE")
	if portText == "" || imagePath == "" {
		t.Skip("live target not configured")
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	library, err := newLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	theme, err := library.add("temporary-check", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := library.setActive(theme.ID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := restoreCodex(ctx, port); err != nil {
			t.Errorf("could not remove temporary style: %v", err)
		}
	}()
	if err := applyToCodex(context.Background(), port, library); err != nil {
		t.Fatal(err)
	}
	if delay, err := strconv.Atoi(os.Getenv("CODEX_POSTER_LIVE_SECONDS")); err == nil && delay > 0 && delay <= 30 {
		time.Sleep(time.Duration(delay) * time.Second)
	}
}
