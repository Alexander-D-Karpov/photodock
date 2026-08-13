package services

import (
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Alexander-D-Karpov/photodock/internal/models"
	"github.com/disintegration/imaging"
)

const (
	stagingPreviewWidth = 400
	stagingViewWidth    = 2560
)

var thumbSizes = []string{"small", "medium", "large"}

type ThumbnailService struct {
	mediaRoot   string
	cacheDir    string
	existsCache sync.Map
}

func NewThumbnailService(mediaRoot, cacheDir string) *ThumbnailService {
	_ = os.MkdirAll(filepath.Join(cacheDir, "small"), 0755)
	_ = os.MkdirAll(filepath.Join(cacheDir, "medium"), 0755)
	_ = os.MkdirAll(filepath.Join(cacheDir, "large"), 0755)
	_ = os.MkdirAll(filepath.Join(cacheDir, "placeholder"), 0755)
	_ = os.MkdirAll(filepath.Join(cacheDir, "raw"), 0755)
	_ = os.MkdirAll(filepath.Join(cacheDir, "rawdev"), 0755)
	rawDevelopers()
	return &ThumbnailService{
		mediaRoot: mediaRoot,
		cacheDir:  cacheDir,
	}
}

func NormalizeRotation(rotation int) int {
	r := rotation % 360
	if r < 0 {
		r += 360
	}
	return (r / 90) * 90
}

// Rotation is stored clockwise; imaging.Rotate90 turns counter-clockwise.
func applyRotation(img image.Image, rotation int) image.Image {
	switch NormalizeRotation(rotation) {
	case 90:
		return imaging.Rotate270(img)
	case 180:
		return imaging.Rotate180(img)
	case 270:
		return imaging.Rotate90(img)
	}
	return img
}

func RotatedDimensions(width, height, rotation int) (int, int) {
	if r := NormalizeRotation(rotation); r == 90 || r == 270 {
		return height, width
	}
	return width, height
}

func resizeToWidth(img image.Image, width int) image.Image {
	if width <= 0 || img.Bounds().Dx() <= width {
		return img
	}
	return imaging.Resize(img, width, 0, imaging.Lanczos)
}

func (s *ThumbnailService) GetThumbnailPathByID(photoID int, photoPath, size string, rotation int) (string, error) {
	ext := ".jpg"
	if strings.HasSuffix(strings.ToLower(photoPath), ".png") {
		ext = ".png"
	}
	thumbPath := filepath.Join(s.cacheDir, size, fmt.Sprintf("%d%s", photoID, ext))

	if _, ok := s.existsCache.Load(thumbPath); ok {
		return thumbPath, nil
	}

	if _, err := os.Stat(thumbPath); err == nil {
		s.existsCache.Store(thumbPath, struct{}{})
		return thumbPath, nil
	}

	srcPath := filepath.Join(s.mediaRoot, photoPath)
	if err := s.generateThumbnail(srcPath, thumbPath, size, rotation); err != nil {
		return "", err
	}

	s.existsCache.Store(thumbPath, struct{}{})
	return thumbPath, nil
}

// RebuildDerivatives drops every cached rendering of a photo and regenerates it
// from the current source and rotation. Thumbnails are keyed by photo ID alone,
// so anything that changes how a photo renders (a rotation edit, a new RAW
// decoder, a render-pipeline change) must go through here or the cached
// thumbnails silently disagree with photos.width/height and with /original/.
func (s *ThumbnailService) RebuildDerivatives(photoID int, photoPath, blurhash string, rotation, width, height int) {
	_ = s.DeleteThumbnailsByID(photoID)
	for _, size := range thumbSizes {
		if _, err := s.GetThumbnailPathByID(photoID, photoPath, size, rotation); err != nil {
			log.Printf("thumbnail %s for photo %d (%s): %v", size, photoID, photoPath, err)
		}
	}
	if _, err := s.GetPlaceholderPathByID(photoID, blurhash, width, height); err != nil {
		log.Printf("placeholder for photo %d (%s): %v", photoID, photoPath, err)
	}
}

func (s *ThumbnailService) generateThumbnail(srcPath, dstPath, size string, rotation int) error {
	img, err := s.openImage(srcPath)
	if err != nil {
		return err
	}
	img = applyRotation(img, rotation)

	var width int
	var quality int
	switch size {
	case "small":
		width = 300
		quality = 80
	case "medium":
		width = 1000
		quality = 86
	case "large":
		width = 2560
		quality = 90
	default:
		width = 300
		quality = 80
	}

	thumb := resizeToWidth(img, width)

	if strings.HasSuffix(strings.ToLower(dstPath), ".png") {
		return imaging.Save(thumb, dstPath)
	}
	return imaging.Save(thumb, dstPath, imaging.JPEGQuality(quality))
}

func (s *ThumbnailService) MakeStagingPreviews(srcPath, dir string, rotation int) (int, int, error) {
	img, err := s.openImage(srcPath)
	if err != nil {
		return 0, 0, err
	}
	img = applyRotation(img, rotation)
	b := img.Bounds()

	view := resizeToWidth(img, stagingViewWidth)
	if err := imaging.Save(view, filepath.Join(dir, "view.jpg"), imaging.JPEGQuality(92)); err != nil {
		return 0, 0, err
	}

	thumb := resizeToWidth(img, stagingPreviewWidth)
	if err := imaging.Save(thumb, filepath.Join(dir, "preview.jpg"), imaging.JPEGQuality(82)); err != nil {
		return 0, 0, err
	}

	return b.Dx(), b.Dy(), nil
}

func (s *ThumbnailService) GenerateBlurhash(photoPath string, rotation int) (string, error) {
	img, err := s.openImage(filepath.Join(s.mediaRoot, photoPath))
	if err != nil {
		return "", err
	}
	img = applyRotation(img, rotation)

	tiny := imaging.Resize(img, 4, 4, imaging.Box)
	bounds := tiny.Bounds()
	var pixels []byte

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := tiny.At(x, y).RGBA()
			pixels = append(pixels, byte(r>>8), byte(g>>8), byte(b>>8))
		}
	}

	return base64.StdEncoding.EncodeToString(pixels), nil
}

func (s *ThumbnailService) GetImageDimensions(photoPath string, rotation int) (int, int, error) {
	img, err := s.openImage(filepath.Join(s.mediaRoot, photoPath))
	if err != nil {
		return 0, 0, err
	}
	b := img.Bounds()
	w, h := RotatedDimensions(b.Dx(), b.Dy(), rotation)
	return w, h, nil
}

func (s *ThumbnailService) GeneratePlaceholder(blurhash string, width, height int) (image.Image, error) {
	data, err := base64.StdEncoding.DecodeString(blurhash)
	if err != nil || len(data) < 48 {
		img := image.NewRGBA(image.Rect(0, 0, width, height))
		gray := color.RGBA{128, 128, 128, 255}
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				img.Set(x, y, gray)
			}
		}
		return img, nil
	}

	tiny := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			idx := (y*4 + x) * 3
			tiny.Set(x, y, color.RGBA{data[idx], data[idx+1], data[idx+2], 255})
		}
	}

	return imaging.Resize(tiny, width, height, imaging.Linear), nil
}

// placeholderDims scales a photo's dimensions down to a tiny box (longest side
// ~64px) preserving aspect ratio, so the placeholder PNG matches the photo's
// shape instead of being a square that gets stretched in the grid. Unknown
// dims fall back to a square.
func placeholderDims(width, height int) (int, int) {
	const maxSide = 64
	if width <= 0 || height <= 0 {
		return 32, 32
	}
	if width >= height {
		h := (height*maxSide + width/2) / width
		if h < 1 {
			h = 1
		}
		return maxSide, h
	}
	w := (width*maxSide + height/2) / height
	if w < 1 {
		w = 1
	}
	return w, maxSide
}

func (s *ThumbnailService) GetPlaceholderPathByID(photoID int, blurhash string, width, height int) (string, error) {
	placeholderPath := filepath.Join(s.cacheDir, "placeholder", fmt.Sprintf("%d.png", photoID))

	if _, ok := s.existsCache.Load(placeholderPath); ok {
		return placeholderPath, nil
	}

	if _, err := os.Stat(placeholderPath); err == nil {
		s.existsCache.Store(placeholderPath, struct{}{})
		return placeholderPath, nil
	}

	pw, ph := placeholderDims(width, height)
	img, err := s.GeneratePlaceholder(blurhash, pw, ph)
	if err != nil {
		return "", err
	}

	f, err := os.Create(placeholderPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	if err := png.Encode(f, img); err != nil {
		return "", err
	}

	s.existsCache.Store(placeholderPath, struct{}{})
	return placeholderPath, nil
}

func (s *ThumbnailService) DeleteThumbnailsByID(photoID int) error {
	for _, size := range []string{"small", "medium", "large", "placeholder"} {
		for _, ext := range []string{".jpg", ".png"} {
			path := filepath.Join(s.cacheDir, size, fmt.Sprintf("%d%s", photoID, ext))
			_ = os.Remove(path)
			s.existsCache.Delete(path)
		}
	}
	return nil
}

func (s *ThumbnailService) PrewarmCache() {
	for _, size := range []string{"small", "medium", "large", "placeholder"} {
		dir := filepath.Join(s.cacheDir, size)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				s.existsCache.Store(filepath.Join(dir, entry.Name()), struct{}{})
			}
		}
	}
}

func (s *ThumbnailService) CacheDir() string {
	return s.cacheDir
}

func (s *ThumbnailService) AnalyzeColors(photoPath string) (*models.ColorInfo, error) {
	img, err := s.openImage(filepath.Join(s.mediaRoot, photoPath))
	if err != nil {
		return nil, err
	}

	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	info := &models.ColorInfo{
		IsLandscape: w > h,
		AspectRatio: float64(w) / float64(h),
		MegaPixels:  float64(w*h) / 1_000_000,
	}

	tiny := imaging.Resize(img, 16, 16, imaging.Box)
	tb := tiny.Bounds()

	var totalR, totalG, totalB float64
	var totalBright float64
	colorCounts := make(map[string]int)
	count := 0

	for y := tb.Min.Y; y < tb.Max.Y; y++ {
		for x := tb.Min.X; x < tb.Max.X; x++ {
			r, g, b, _ := tiny.At(x, y).RGBA()
			fr, fg, fb := float64(r>>8), float64(g>>8), float64(b>>8)
			totalR += fr
			totalG += fg
			totalB += fb
			totalBright += (0.299*fr + 0.587*fg + 0.114*fb) / 255.0

			qr := int(fr/32) * 32
			qg := int(fg/32) * 32
			qb := int(fb/32) * 32
			key := fmt.Sprintf("#%02x%02x%02x", qr, qg, qb)
			colorCounts[key]++
			count++
		}
	}

	if count > 0 {
		info.AvgBrightness = totalBright / float64(count)
		info.DominantColor = fmt.Sprintf("#%02x%02x%02x",
			int(totalR/float64(count)),
			int(totalG/float64(count)),
			int(totalB/float64(count)))
	}

	type colorEntry struct {
		hex   string
		count int
	}
	var entries []colorEntry
	for k, v := range colorCounts {
		entries = append(entries, colorEntry{k, v})
	}
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].count > entries[i].count {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}
	for i := 0; i < len(entries) && i < 5; i++ {
		info.Palette = append(info.Palette, entries[i].hex)
	}

	return info, nil
}
