package services

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// SetPrivacy makes private photos' derivatives live under an HMAC-keyed name nginx can't guess.
func (s *ThumbnailService) SetPrivacy(secret string, isPrivate func(photoID int) bool) {
	s.secret = []byte(secret)
	s.isPrivate = isPrivate
}

// SecretKey is an unguessable, stable token for a private object, e.g. ("photo", 42).
func (s *ThumbnailService) SecretKey(kind string, id int) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(kind + ":" + strconv.Itoa(id)))
	return hex.EncodeToString(mac.Sum(nil))[:32]
}

func (s *ThumbnailService) privateStem(photoID int) string {
	return fmt.Sprintf("%d-%s", photoID, s.SecretKey("photo", photoID))
}

// stem is the cache file name (without extension) for a photo's derivatives.
func (s *ThumbnailService) stem(photoID int) string {
	if s.isPrivate != nil && len(s.secret) > 0 && s.isPrivate(photoID) {
		return s.privateStem(photoID)
	}
	return strconv.Itoa(photoID)
}

// SyncPrivacy renames a photo's cached derivatives to match its current privacy.
func (s *ThumbnailService) SyncPrivacy(photoID int) {
	if len(s.secret) == 0 {
		return
	}
	from, to := strconv.Itoa(photoID), s.privateStem(photoID)
	if s.stem(photoID) == from {
		from, to = to, from
	}
	move := func(a, b string) {
		if _, err := os.Stat(a); err != nil {
			return
		}
		_ = os.RemoveAll(b)
		_ = os.Rename(a, b)
		s.existsCache.Delete(a)
		s.existsCache.Delete(b)
	}
	for _, dir := range []string{"small", "medium", "large", "placeholder"} {
		for _, ext := range []string{".jpg", ".png"} {
			move(filepath.Join(s.cacheDir, dir, from+ext), filepath.Join(s.cacheDir, dir, to+ext))
		}
	}
	move(filepath.Join(s.cacheDir, "develop", from), filepath.Join(s.cacheDir, "develop", to))
}
