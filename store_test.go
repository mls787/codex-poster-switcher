package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	imageData := image.NewRGBA(image.Rect(0, 0, 320, 180))
	imageData.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, imageData); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestLibraryPersistsAndDeletes(t *testing.T) {
	dir := t.TempDir()
	library, err := newLibrary(dir)
	if err != nil {
		t.Fatal(err)
	}
	theme, err := library.add("我的海报", bytes.NewReader(testPNG(t)))
	if err != nil {
		t.Fatal(err)
	}
	if err := library.setActive(theme.ID); err != nil {
		t.Fatal(err)
	}
	loaded, err := newLibrary(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.snapshot(); got.Active != theme.ID || len(got.Themes) != 1 {
		t.Fatalf("library was not persisted: %+v", got)
	}
	if err := loaded.delete(theme.ID); err != nil {
		t.Fatal(err)
	}
	if got := loaded.snapshot(); got.Active != "" || len(got.Themes) != 0 {
		t.Fatalf("theme was not removed: %+v", got)
	}
	if _, _, err := loaded.get(theme.ID); !os.IsNotExist(err) {
		t.Fatalf("deleted image still readable: %v", err)
	}
}

func TestRejectsNonImageAndTooLarge(t *testing.T) {
	library, err := newLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{[]byte("<svg xmlns='http://www.w3.org/2000/svg'></svg>"), bytes.Repeat([]byte("x"), maxImageBytes+1)} {
		if _, err := library.add("bad", bytes.NewReader(data)); err == nil {
			t.Fatal("invalid image was accepted")
		}
	}
	if len(library.snapshot().Themes) != 0 {
		t.Fatal("invalid image changed the library")
	}
}
