package services

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"image"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disintegration/imaging"
)

const (
	rawDevQuality      = 92
	rawDevTimeout      = 5 * time.Minute
	RawDevTimeoutSlack = rawDevTimeout
	rawRenderVersion   = 2
)

func isRawPath(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".arw")
}

type rawDeveloper struct {
	name string
	run  func(bin, src, dstJPEG, work string) (string, error)
}

var allRawDevelopers = []rawDeveloper{
	{name: "darktable-cli", run: developDarktable},
	{name: "rawtherapee-cli", run: developRawtherapee},
	{name: "dcraw_emu", run: developDcrawEmu},
	{name: "dcraw", run: developDcraw},
}

type resolvedDeveloper struct {
	rawDeveloper
	bin string
}

var (
	rawDevOnce sync.Once
	rawDevs    []resolvedDeveloper
	rawLocks   sync.Map
)

func rawDevelopers() []resolvedDeveloper {
	rawDevOnce.Do(func() {
		want := strings.TrimSpace(os.Getenv("RAW_DECODER"))
		for _, d := range allRawDevelopers {
			if want != "" && want != d.name {
				continue
			}
			bin, err := exec.LookPath(d.name)
			if err != nil {
				continue
			}
			rawDevs = append(rawDevs, resolvedDeveloper{rawDeveloper: d, bin: bin})
		}
		if len(rawDevs) == 0 {
			log.Printf("raw: no full-resolution decoder on PATH; ARW falls back to the small embedded preview")
			return
		}
		names := make([]string, 0, len(rawDevs))
		for _, d := range rawDevs {
			names = append(names, d.name)
		}
		log.Printf("raw: full-resolution decoders in order: %s", strings.Join(names, ", "))
	})
	return rawDevs
}

func rawLock(key string) *sync.Mutex {
	v, _ := rawLocks.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func runRawCmd(bin string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), rawDevTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return fmt.Errorf("%s: %w (%s)", filepath.Base(bin), err, msg)
	}
	return nil
}

func developDarktable(bin, src, dst, work string) (string, error) {
	cfg := filepath.Join(work, "dt")
	if err := os.MkdirAll(cfg, 0700); err != nil {
		return "", err
	}
	err := runRawCmd(bin, src, dst,
		"--apply-custom-presets", "false",
		"--hq", "true",
		"--core",
		"--library", ":memory:",
		"--configdir", cfg,
		"--cachedir", cfg,
		"--disable-opencl",
		"--conf", "plugins/imageio/format/jpeg/quality="+strconv.Itoa(rawDevQuality))
	if err != nil {
		return "", err
	}
	return dst, nil
}

func developRawtherapee(bin, src, dst, work string) (string, error) {
	err := runRawCmd(bin, "-Y", "-o", dst, "-j"+strconv.Itoa(rawDevQuality), "-js3", "-d", "-c", src)
	if err != nil {
		return "", err
	}
	return dst, nil
}

// dcraw_emu names its output after the input, so the source is symlinked into a
// scratch dir: writing next to the original would drop a .tiff into MEDIA_ROOT
// and the scanner would pick it up as a photo.
func developDcrawEmu(bin, src, dst, work string) (string, error) {
	link := filepath.Join(work, "in"+filepath.Ext(src))
	if err := os.Symlink(src, link); err != nil {
		return "", err
	}
	if err := runRawCmd(bin, "-w", "-q", "3", "-T", link); err != nil {
		return "", err
	}
	out := link + ".tiff"
	if _, err := os.Stat(out); err != nil {
		return "", fmt.Errorf("dcraw_emu produced no tiff: %w", err)
	}
	return out, nil
}

func developDcraw(bin, src, dst, work string) (string, error) {
	out := filepath.Join(work, "out.tiff")
	f, err := os.Create(out)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), rawDevTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-c", "-w", "-q", "3", "-T", src)
	cmd.Stdout = f
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	_ = f.Close()
	if err != nil {
		return "", fmt.Errorf("dcraw: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	if fi, serr := os.Stat(out); serr != nil || fi.Size() == 0 {
		return "", fmt.Errorf("dcraw produced no output")
	}
	return out, nil
}

func rawCacheKey(absPath string, rotation int) (string, error) {
	fi, err := os.Stat(absPath)
	if err != nil {
		return "", err
	}
	h := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d|%d|%d|%d",
		absPath, fi.Size(), fi.ModTime().UnixNano(), rotation, rawDevQuality, rawRenderVersion)))
	return hex.EncodeToString(h[:])[:20], nil
}

// exifOrientation reads the camera's orientation flag numerically. Returns 0
// when exiftool is unavailable or the tag is absent.
func exifOrientation(path string) int {
	out, err := exec.Command("exiftool", "-s3", "-n", "-Orientation", path).Output()
	if err != nil {
		return 0
	}
	line := strings.TrimSpace(string(out))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	v, err := strconv.Atoi(line)
	if err != nil {
		return 0
	}
	return v
}

func orientImage(img image.Image, orientation int) image.Image {
	switch orientation {
	case 2:
		return imaging.FlipH(img)
	case 3:
		return imaging.Rotate180(img)
	case 4:
		return imaging.FlipV(img)
	case 5:
		return imaging.Transpose(img)
	case 6:
		return imaging.Rotate270(img)
	case 7:
		return imaging.Transverse(img)
	case 8:
		return imaging.Rotate90(img)
	}
	return img
}

// fixRawOrientation compensates for decoders that ignore the camera's
// orientation flag. Sensors are landscape, so an orientation of 5-8 means the
// displayed frame must be portrait; a still-landscape render means the turn was
// not applied (or was applied twice) and has to be redone here.
func fixRawOrientation(img image.Image, orientation int) image.Image {
	if orientation < 5 || orientation > 8 {
		return img
	}
	b := img.Bounds()
	if b.Dx() <= b.Dy() {
		return img
	}
	return orientImage(img, orientation)
}

// normalizeRawRender re-encodes a decoder's output into a plain JPEG with the
// orientation baked into the pixels and no EXIF left behind, so thumbnails
// (which read it through imaging) and browsers (which get the file verbatim
// from /original/) can never disagree about which way is up.
func normalizeRawRender(src, dst string, orientation int) error {
	img, err := imaging.Open(src, imaging.AutoOrientation(true))
	if err != nil {
		return err
	}
	img = fixRawOrientation(img, orientation)
	return imaging.Save(img, dst, imaging.JPEGQuality(rawDevQuality))
}

func extractRawPreview(srcPath, cacheDir string) (string, func(), error) {
	rawDir := filepath.Join(cacheDir, "raw")
	if err := os.MkdirAll(rawDir, 0755); err != nil {
		return "", func() {}, err
	}

	tmp, err := os.CreateTemp(rawDir, "raw-*.jpg")
	if err != nil {
		return "", func() {}, err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	cleanup := func() { _ = os.Remove(tmpPath) }

	for _, tag := range []string{"-JpgFromRaw", "-PreviewImage", "-ThumbnailImage"} {
		cmd := exec.Command("exiftool", "-b", tag, srcPath)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil || out.Len() == 0 {
			continue
		}
		if err := os.WriteFile(tmpPath, out.Bytes(), 0644); err != nil {
			cleanup()
			return "", func() {}, err
		}
		return tmpPath, cleanup, nil
	}

	cleanup()
	return "", func() {}, os.ErrNotExist
}

// rawBase returns a cached, full-resolution, correctly oriented JPEG rendering
// of a RAW file, developing it on first use. Falls back to the embedded preview
// when no decoder is installed.
func (s *ThumbnailService) rawBase(absPath string) (string, error) {
	key, err := rawCacheKey(absPath, 0)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(s.cacheDir, "rawdev", key+".jpg")

	mu := rawLock(dst)
	mu.Lock()
	defer mu.Unlock()

	if fi, err := os.Stat(dst); err == nil && fi.Size() > 0 {
		return dst, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return "", err
	}

	work, err := os.MkdirTemp(filepath.Join(s.cacheDir, "raw"), "dev-")
	if err != nil {
		if merr := os.MkdirAll(filepath.Join(s.cacheDir, "raw"), 0755); merr != nil {
			return "", merr
		}
		if work, err = os.MkdirTemp(filepath.Join(s.cacheDir, "raw"), "dev-"); err != nil {
			return "", err
		}
	}
	defer func() { _ = os.RemoveAll(work) }()

	orientation := exifOrientation(absPath)
	tmpOut := filepath.Join(work, "out.jpg")
	normalized := filepath.Join(work, "normalized.jpg")

	for _, d := range rawDevelopers() {
		started := time.Now()
		produced, derr := d.run(d.bin, absPath, tmpOut, work)
		if derr != nil {
			log.Printf("raw: %s failed on %s: %v", d.name, filepath.Base(absPath), derr)
			continue
		}
		if nerr := normalizeRawRender(produced, normalized, orientation); nerr != nil {
			log.Printf("raw: cannot normalize %s output for %s: %v", d.name, filepath.Base(absPath), nerr)
			continue
		}
		if rerr := os.Rename(normalized, dst); rerr != nil {
			return "", rerr
		}
		log.Printf("raw: developed %s with %s (orientation %d) in %s",
			filepath.Base(absPath), d.name, orientation, time.Since(started).Round(time.Millisecond))
		return dst, nil
	}

	preview, cleanup, perr := extractRawPreview(absPath, s.cacheDir)
	if perr != nil {
		return "", fmt.Errorf("no raw decoder and no embedded preview for %s: %w", filepath.Base(absPath), perr)
	}
	defer cleanup()
	if nerr := normalizeRawRender(preview, normalized, orientation); nerr != nil {
		return "", nerr
	}
	return dst, os.Rename(normalized, dst)
}

// RawWebPath returns a browser-viewable full-resolution JPEG for a RAW file,
// with the stored rotation applied on top of the normalized base.
func (s *ThumbnailService) RawWebPath(absPath string, rotation int) (string, error) {
	base, err := s.rawBase(absPath)
	if err != nil {
		return "", err
	}
	if NormalizeRotation(rotation) == 0 {
		return base, nil
	}

	key, err := rawCacheKey(absPath, rotation)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(s.cacheDir, "rawdev", key+".jpg")

	mu := rawLock(dst)
	mu.Lock()
	defer mu.Unlock()

	if fi, err := os.Stat(dst); err == nil && fi.Size() > 0 {
		return dst, nil
	}

	img, err := imaging.Open(base, imaging.AutoOrientation(true))
	if err != nil {
		return "", err
	}
	tmp := dst + ".tmp"
	if err := imaging.Save(applyRotation(img, rotation), tmp, imaging.JPEGQuality(rawDevQuality)); err != nil {
		return "", err
	}
	return dst, os.Rename(tmp, dst)
}

func (s *ThumbnailService) openImage(absPath string) (image.Image, error) {
	if isRawPath(absPath) {
		dev, err := s.rawBase(absPath)
		if err != nil {
			return nil, err
		}
		return imaging.Open(dev, imaging.AutoOrientation(true))
	}
	return imaging.Open(absPath, imaging.AutoOrientation(true))
}

func (s *ThumbnailService) MakePreview(srcPath, dstPath string, width int) (int, int, error) {
	img, err := s.openImage(srcPath)
	if err != nil {
		return 0, 0, err
	}
	b := img.Bounds()
	ow, oh := b.Dx(), b.Dy()
	thumb := resizeToWidth(img, width)
	if err := imaging.Save(thumb, dstPath, imaging.JPEGQuality(82)); err != nil {
		return 0, 0, err
	}
	return ow, oh, nil
}
