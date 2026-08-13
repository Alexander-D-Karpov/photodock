package services

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOrientedDimensions(t *testing.T) {
	tests := []struct {
		name         string
		w, h         int
		orientation  int
		wantW, wantH int
	}{
		{"no exif (orientation 0) unchanged", 4608, 3456, 0, 4608, 3456},
		{"orientation 1 (normal) unchanged", 4608, 3456, 1, 4608, 3456},
		{"orientation 3 (180) unchanged", 4608, 3456, 3, 4608, 3456},
		{"orientation 4 unchanged", 4608, 3456, 4, 4608, 3456},
		{"orientation 5 swaps", 4608, 3456, 5, 3456, 4608},
		{"orientation 6 swaps (Canon portrait)", 4608, 3456, 6, 3456, 4608},
		{"orientation 7 swaps", 4608, 3456, 7, 3456, 4608},
		{"orientation 8 swaps", 4608, 3456, 8, 3456, 4608},
		{"orientation 9 (out of range) unchanged", 4608, 3456, 9, 4608, 3456},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotW, gotH := orientedDimensions(tt.w, tt.h, tt.orientation)
			if gotW != tt.wantW || gotH != tt.wantH {
				t.Errorf("orientedDimensions(%d,%d,%d) = (%d,%d); want (%d,%d)",
					tt.w, tt.h, tt.orientation, gotW, gotH, tt.wantW, tt.wantH)
			}
		})
	}
}

func TestIsImageFileARW(t *testing.T) {
	for _, n := range []string{"x.arw", "x.ARW", "dir/y.Arw", "x.jpg", "x.jpeg", "x.png", "X.JPG"} {
		if !isImageFile(n) {
			t.Errorf("isImageFile(%q) = false; want true", n)
		}
	}
	for _, n := range []string{"x.cr2", "x.nef", "x.gif", "x.txt", "x", "arw"} {
		if isImageFile(n) {
			t.Errorf("isImageFile(%q) = true; want false", n)
		}
	}
}

func TestFileHash(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if err := os.WriteFile(p, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := fileHash(p)
	if err != nil {
		t.Fatal(err)
	}
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got != want {
		t.Errorf("fileHash = %s; want %s", got, want)
	}
	if _, err := fileHash(filepath.Join(dir, "nope")); err == nil {
		t.Error("expected error for missing file")
	}
}
