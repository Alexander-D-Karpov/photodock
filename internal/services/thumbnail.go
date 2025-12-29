package services

import (
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/disintegration/imaging"
)

type ThumbnailService struct {
	mediaRoot   string
	cacheDir    string
	existsCache sync.Map
}

func NewThumbnailService(mediaRoot, cacheDir string) *ThumbnailService {
	os.MkdirAll(filepath.Join(cacheDir, "small"), 0755)
	os.MkdirAll(filepath.Join(cacheDir, "medium"), 0755)
	os.MkdirAll(filepath.Join(cacheDir, "large"), 0755)
	os.MkdirAll(filepath.Join(cacheDir, "placeholder"), 0755)
	return &ThumbnailService{
		mediaRoot: mediaRoot,
		cacheDir:  cacheDir,
	}
}

func (s *ThumbnailService) GetThumbnailPathByID(photoID int, photoPath, size string) (string, error) {
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
	if err := s.generateThumbnail(srcPath, thumbPath, size); err != nil {
		return "", err
	}

	s.existsCache.Store(thumbPath, struct{}{})
	return thumbPath, nil
}

func (s *ThumbnailService) generateThumbnail(srcPath, dstPath, size string) error {
	img, err := imaging.Open(srcPath, imaging.AutoOrientation(true))
	if err != nil {
		return err
	}

	var width int
	var quality int
	switch size {
	case "small":
		width = 300
		quality = 80
	case "medium":
		width = 800
		quality = 85
	case "large":
		width = 1440
		quality = 85
	default:
		width = 300
		quality = 80
	}

	thumb := imaging.Resize(img, width, 0, imaging.Lanczos)

	if strings.HasSuffix(strings.ToLower(dstPath), ".png") {
		return imaging.Save(thumb, dstPath)
	}
	return imaging.Save(thumb, dstPath, imaging.JPEGQuality(quality))
}

func (s *ThumbnailService) GenerateBlurhash(photoPath string) (string, error) {
	srcPath := filepath.Join(s.mediaRoot, photoPath)
	img, err := imaging.Open(srcPath, imaging.AutoOrientation(true))
	if err != nil {
		return "", err
	}

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

func (s *ThumbnailService) GetImageDimensions(photoPath string) (int, int, error) {
	srcPath := filepath.Join(s.mediaRoot, photoPath)
	f, err := os.Open(srcPath)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	config, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, err
	}

	return config.Width, config.Height, nil
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

func (s *ThumbnailService) GetPlaceholderPathByID(photoID int, blurhash string) (string, error) {
	placeholderPath := filepath.Join(s.cacheDir, "placeholder", fmt.Sprintf("%d.png", photoID))

	if _, ok := s.existsCache.Load(placeholderPath); ok {
		return placeholderPath, nil
	}

	if _, err := os.Stat(placeholderPath); err == nil {
		s.existsCache.Store(placeholderPath, struct{}{})
		return placeholderPath, nil
	}

	img, err := s.GeneratePlaceholder(blurhash, 32, 32)
	if err != nil {
		return "", err
	}

	f, err := os.Create(placeholderPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

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
			os.Remove(path)
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
