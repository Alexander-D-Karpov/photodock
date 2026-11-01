package services

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/disintegration/imaging"
)

func TestPlaceholderDims(t *testing.T) {
	cases := []struct {
		w, h         int
		wantW, wantH int
	}{
		{4000, 3000, 64, 48}, // landscape 4:3
		{3000, 4000, 48, 64}, // portrait 3:4
		{6024, 4024, 64, 43}, // Sony ARW landscape
		{4024, 6024, 43, 64}, // rotated portrait
		{1000, 1000, 64, 64}, // square
		{6400, 100, 64, 1},   // extreme wide clamps to >=1
		{0, 0, 32, 32},       // unknown dims fall back to square
		{-5, 10, 32, 32},     // invalid dims fall back
	}
	for _, c := range cases {
		gotW, gotH := placeholderDims(c.w, c.h)
		if gotW != c.wantW || gotH != c.wantH {
			t.Errorf("placeholderDims(%d,%d) = %dx%d; want %dx%d", c.w, c.h, gotW, gotH, c.wantW, c.wantH)
		}
		// Placeholder must preserve the photo's orientation: never square-ify a non-square photo.
		if c.w > 0 && c.h > 0 && c.w != c.h {
			photoLandscape := c.w > c.h
			phLandscape := gotW > gotH
			if photoLandscape != phLandscape {
				t.Errorf("placeholderDims(%d,%d) = %dx%d flipped orientation", c.w, c.h, gotW, gotH)
			}
		}
	}
}

func TestResponsiveThumbnailSizes(t *testing.T) {
	for _, size := range thumbSizes {
		if width, ok := ThumbnailWidth(size); !ok || width <= 0 {
			t.Errorf("invalid configured size %q: %d, %v", size, width, ok)
		}
	}
	for _, size := range []string{"", "w321", "../large", "w999999"} {
		if _, ok := ThumbnailWidth(size); ok {
			t.Errorf("unexpectedly accepted size %q", size)
		}
	}
}

func TestResponsiveThumbnails(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	svc := NewThumbnailService(root, cache)
	src := imaging.New(900, 600, color.NRGBA{R: 230, G: 10, B: 20, A: 255})
	if err := imaging.Save(src, filepath.Join(root, "photo.jpg")); err != nil {
		t.Fatal(err)
	}

	path, err := svc.GetThumbnailPathByID(1, "photo.jpg", "w640", 0)
	if err != nil {
		t.Fatal(err)
	}
	assertImageSize(t, path, 640, 427)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0644 {
		t.Fatalf("thumbnail permissions = %v, err = %v", info, err)
	}

	path, err = svc.GetThumbnailPathByID(2, "photo.jpg", "w320", 90)
	if err != nil {
		t.Fatal(err)
	}
	assertImageSize(t, path, 320, 480)

	path, err = svc.GetThumbnailPathByID(3, "photo.jpg", "w1280", 0)
	if err != nil {
		t.Fatal(err)
	}
	assertImageSize(t, path, 900, 600) // Never upscale the source.

	if _, err := svc.GetThumbnailPathByID(1, "photo.jpg", "../w640", 0); err == nil {
		t.Fatal("accepted an invalid thumbnail size")
	}
	if _, err := svc.GetThumbnailPathByID(4, "photo.jpg", "large", 90); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "photo.jpg")); err != nil {
		t.Fatal(err)
	}
	path, err = svc.GetThumbnailPathByID(4, "photo.jpg", "w320", 90)
	if err != nil {
		t.Fatalf("could not reuse the existing large derivative: %v", err)
	}
	assertImageSize(t, path, 320, 480) // The already-oriented derivative must not be rotated twice.
	if err := svc.DeleteThumbnailsByID(1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cache, "w640", "1.jpg")); !os.IsNotExist(err) {
		t.Fatalf("responsive thumbnail was not deleted: %v", err)
	}
}

func TestEditedResponsiveThumbnailAndConcurrentRequests(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	svc := NewThumbnailService(root, cache)
	dir := filepath.Join(cache, "develop", "1", "0123456789abcdef")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(dir, "full.jpg")
	if err := imaging.Save(imaging.New(900, 600, color.NRGBA{B: 240, A: 255}), full); err != nil {
		t.Fatal(err)
	}

	const requests = 8
	var wg sync.WaitGroup
	errs := make(chan error, requests)
	for range requests {
		wg.Go(func() {
			_, err := svc.GetEditedThumbnailPath(1, full, "w320")
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "w320.jpg")
	assertImageSize(t, path, 320, 213)
	img, err := imaging.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r, _, b, _ := img.At(0, 0).RGBA()
	if b <= r {
		t.Fatal("responsive thumbnail did not use the published edit")
	}
	// A re-published directory may discard responsive files even with the same hash.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetEditedThumbnailPath(1, full, "w320"); err != nil {
		t.Fatalf("regenerating removed edited thumbnail: %v", err)
	}
	assertImageSize(t, path, 320, 213)
}

func TestResponsivePrivateCache(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	svc := NewThumbnailService(root, cache)
	if err := imaging.Save(imaging.New(400, 300, color.NRGBA{A: 255}), filepath.Join(root, "photo.png")); err != nil {
		t.Fatal(err)
	}
	private := false
	svc.SetPrivacy("test secret", func(int) bool { return private })
	path, err := svc.GetThumbnailPathByID(5, "photo.png", "w320", 0)
	if err != nil {
		t.Fatal(err)
	}
	private = true
	svc.SyncPrivacy(5)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("public thumbnail still exists: %v", err)
	}
	privatePath, err := svc.GetThumbnailPathByID(5, "photo.png", "w320", 0)
	if err != nil || privatePath == path {
		t.Fatalf("private path = %q, err = %v", privatePath, err)
	}
	assertImageSize(t, privatePath, 320, 240)
}

func TestPrivacyChangeWaitsForResponsiveGeneration(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	svc := NewThumbnailService(root, cache)
	if err := imaging.Save(imaging.New(400, 300, color.NRGBA{A: 255}), filepath.Join(root, "photo.jpg")); err != nil {
		t.Fatal(err)
	}
	var private atomic.Bool
	entered := make(chan struct{}, 1)
	svc.SetPrivacy("test secret", func(int) bool {
		isPrivate := private.Load()
		select {
		case entered <- struct{}{}:
		default:
		}
		return isPrivate
	})
	// Occupy the generation slots so the public request is in flight as privacy changes.
	svc.thumbnailSlots <- struct{}{}
	svc.thumbnailSlots <- struct{}{}
	done := make(chan error, 1)
	go func() {
		_, err := svc.GetThumbnailPathByID(5, "photo.jpg", "w320", 0)
		done <- err
	}()
	<-entered
	private.Store(true)
	synced := make(chan struct{})
	go func() {
		svc.SyncPrivacy(5)
		close(synced)
	}()
	<-svc.thumbnailSlots
	<-svc.thumbnailSlots
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	<-synced
	if _, err := os.Stat(filepath.Join(cache, "w320", "5.jpg")); !os.IsNotExist(err) {
		t.Fatalf("public thumbnail left behind after privacy change: %v", err)
	}
	assertImageSize(t, filepath.Join(cache, "w320", svc.privateStem(5)+".jpg"), 320, 240)
}

func assertImageSize(t *testing.T, path string, width, height int) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	config, _, err := image.DecodeConfig(f)
	if err != nil || config.Width != width || config.Height != height {
		t.Fatalf("%s dimensions = %dx%d, err = %v; want %dx%d", path, config.Width, config.Height, err, width, height)
	}
}
