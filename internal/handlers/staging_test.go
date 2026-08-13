package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Alexander-D-Karpov/photodock/internal/config"
)

func TestIsImageFileARWHandlers(t *testing.T) {
	for _, n := range []string{"x.arw", "x.ARW", "x.jpg", "x.png", "x.jpeg"} {
		if !isImageFile(n) {
			t.Errorf("isImageFile(%q) = false; want true", n)
		}
	}
	if isImageFile("x.cr2") || isImageFile("x.txt") {
		t.Error("isImageFile accepted an unsupported extension")
	}
}

func TestIsRawExt(t *testing.T) {
	if !isRawExt("FOO.ARW") || !isRawExt("a/b.arw") {
		t.Error("isRawExt rejected a valid ARW name")
	}
	if isRawExt("foo.jpg") || isRawExt("arw") {
		t.Error("isRawExt accepted a non-ARW name")
	}
}

func TestStagingIDGen(t *testing.T) {
	seen := make(map[string]bool, 500)
	for i := 0; i < 500; i++ {
		id := stagingIDGen()
		if !strings.Contains(id, "-") {
			t.Fatalf("id %q missing separator", id)
		}
		if strings.ContainsAny(id, "/\\ ..") {
			t.Fatalf("id %q contains path-unsafe chars", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestStagingDir(t *testing.T) {
	h := &Handlers{cfg: &config.Config{CacheDir: "/var/cache/pd"}}
	got := h.stagingDir("abc123")
	want := filepath.Join("/var/cache/pd", "staging", "abc123")
	if got != want {
		t.Errorf("stagingDir = %q; want %q", got, want)
	}
}

func TestMoveFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.jpg")
	dst := filepath.Join(dir, "sub", "dst.jpg")
	if err := os.WriteFile(src, []byte("data123"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		t.Fatal(err)
	}
	if err := moveFile(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source should be gone after move")
	}
	if b, err := os.ReadFile(dst); err != nil || string(b) != "data123" {
		t.Errorf("dst content = %q err = %v; want %q", b, err, "data123")
	}
}
