package services

import (
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIsRawPath(t *testing.T) {
	cases := map[string]bool{
		"a.arw": true, "a.ARW": true, "dir/b.Arw": true,
		"a.jpg": false, "a.arw.jpg": false, "arw": false, "": false,
	}
	for in, want := range cases {
		if got := isRawPath(in); got != want {
			t.Errorf("isRawPath(%q) = %v; want %v", in, got, want)
		}
	}
}

func writeTestJPEG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 128, 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
}

// openImage (non-RAW branch) + MakePreview. Needs the imaging dependency (always
// present in-repo). No DB, no exiftool.
func TestOpenImageAndMakePreviewJPEG(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.jpg")
	writeTestJPEG(t, src, 200, 120)

	svc := NewThumbnailService(dir, filepath.Join(dir, "cache"))

	if _, err := svc.openImage(src); err != nil {
		t.Fatalf("openImage(jpeg): %v", err)
	}

	dst := filepath.Join(dir, "preview.jpg")
	ow, oh, err := svc.MakePreview(src, dst, 100)
	if err != nil {
		t.Fatalf("MakePreview: %v", err)
	}
	if ow != 200 || oh != 120 {
		t.Errorf("MakePreview returned source dims (%d,%d); want (200,120)", ow, oh)
	}

	f, err := os.Open(dst)
	if err != nil {
		t.Fatalf("open preview: %v", err)
	}
	defer func() { _ = f.Close() }()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if cfg.Width != 100 {
		t.Errorf("preview width = %d; want 100", cfg.Width)
	}
}

// Missing/invalid RAW: extractRawPreview must error and leave no temp behind.
func TestExtractRawPreviewErrorAndCleanup(t *testing.T) {
	cache := t.TempDir()
	if _, _, err := extractRawPreview(filepath.Join(cache, "missing.arw"), cache); err == nil {
		t.Error("expected error for missing raw file")
	}
	entries, _ := os.ReadDir(filepath.Join(cache, "raw"))
	if len(entries) != 0 {
		t.Errorf("temp files leaked: %d", len(entries))
	}
}

// Happy path. Skips unless exiftool is installed AND you drop a real Sony file
// at internal/services/testdata/sample.arw.
func TestExtractRawPreviewFromFixture(t *testing.T) {
	if _, err := exec.LookPath("exiftool"); err != nil {
		t.Skip("exiftool not installed")
	}
	fixture := filepath.Join("testdata", "sample.arw")
	if _, err := os.Stat(fixture); err != nil {
		t.Skip("no testdata/sample.arw fixture")
	}
	cache := t.TempDir()
	jpg, cleanup, err := extractRawPreview(fixture, cache)
	if err != nil {
		t.Fatalf("extractRawPreview: %v", err)
	}
	defer cleanup()

	f, err := os.Open(jpg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatalf("extracted preview is not a decodable image: %v", err)
	}
	if cfg.Width == 0 || cfg.Height == 0 {
		t.Errorf("preview has zero dimensions: %dx%d", cfg.Width, cfg.Height)
	}
}
