package services

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/disintegration/imaging"
)

type ThumbnailService struct {
	mediaRoot string
	cacheDir  string
}

func NewThumbnailService(mediaRoot, cacheDir string) *ThumbnailService {
	os.MkdirAll(filepath.Join(cacheDir, "small"), 0755)
	os.MkdirAll(filepath.Join(cacheDir, "medium"), 0755)
	return &ThumbnailService{mediaRoot: mediaRoot, cacheDir: cacheDir}
}

func (s *ThumbnailService) GetThumbnailPath(photoPath, size string) (string, error) {
	hash := sha256.Sum256([]byte(photoPath))
	hashStr := fmt.Sprintf("%x", hash[:16])
	ext := ".jpg"
	if strings.HasSuffix(strings.ToLower(photoPath), ".png") {
		ext = ".png"
	}
	thumbPath := filepath.Join(s.cacheDir, size, hashStr+ext)

	if _, err := os.Stat(thumbPath); err == nil {
		return thumbPath, nil
	}

	srcPath := filepath.Join(s.mediaRoot, photoPath)
	if err := s.generateThumbnail(srcPath, thumbPath, size); err != nil {
		return "", err
	}

	return thumbPath, nil
}

func (s *ThumbnailService) generateThumbnail(srcPath, dstPath, size string) error {
	img, err := imaging.Open(srcPath, imaging.AutoOrientation(true))
	if err != nil {
		return err
	}

	var width int
	switch size {
	case "small":
		width = 300
	case "medium":
		width = 800
	default:
		width = 300
	}

	thumb := imaging.Resize(img, width, 0, imaging.Lanczos)

	if strings.HasSuffix(strings.ToLower(dstPath), ".png") {
		return imaging.Save(thumb, dstPath)
	}
	return imaging.Save(thumb, dstPath, imaging.JPEGQuality(85))
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

func (s *ThumbnailService) GetPlaceholderPath(photoPath string, blurhash string) (string, error) {
	hash := sha256.Sum256([]byte(photoPath))
	hashStr := fmt.Sprintf("%x", hash[:16])
	placeholderPath := filepath.Join(s.cacheDir, "placeholder", hashStr+".png")

	if _, err := os.Stat(placeholderPath); err == nil {
		return placeholderPath, nil
	}

	os.MkdirAll(filepath.Join(s.cacheDir, "placeholder"), 0755)

	img, err := s.GeneratePlaceholder(blurhash, 32, 32)
	if err != nil {
		return "", err
	}

	f, err := os.Create(placeholderPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	return placeholderPath, png.Encode(f, img)
}

func (s *ThumbnailService) DeleteThumbnails(photoPath string) error {
	hash := sha256.Sum256([]byte(photoPath))
	hashStr := fmt.Sprintf("%x", hash[:16])

	for _, size := range []string{"small", "medium", "placeholder"} {
		for _, ext := range []string{".jpg", ".png"} {
			path := filepath.Join(s.cacheDir, size, hashStr+ext)
			os.Remove(path)
		}
	}
	return nil
}

func (s *ThumbnailService) CacheDir() string {
	return s.cacheDir
}
