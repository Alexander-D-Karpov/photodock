package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

type Config struct {
	DatabaseURL string
	MediaRoot   string
	CacheDir    string
	ListenAddr  string
	AdminUser   string
	AdminPass   string
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	mediaRoot := os.Getenv("MEDIA_ROOT")
	if mediaRoot == "" {
		return nil, fmt.Errorf("MEDIA_ROOT is required")
	}
	mediaRootAbs, err := filepath.Abs(mediaRoot)
	if err != nil {
		return nil, err
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	cacheDir := os.Getenv("CACHE_DIR")
	if cacheDir == "" {
		cacheDir = filepath.Join(mediaRootAbs, ".photodock_cache")
	}
	cacheDirAbs, err := filepath.Abs(cacheDir)
	if err != nil {
		return nil, err
	}

	listenAddr := os.Getenv("LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":8080"
	}

	adminUser := os.Getenv("ADMIN_USER")
	if adminUser == "" {
		adminUser = "admin"
	}

	adminPass := os.Getenv("ADMIN_PASS")
	if adminPass == "" {
		return nil, fmt.Errorf("ADMIN_PASS is required")
	}

	return &Config{
		DatabaseURL: dbURL,
		MediaRoot:   mediaRootAbs,
		CacheDir:    cacheDirAbs,
		ListenAddr:  listenAddr,
		AdminUser:   adminUser,
		AdminPass:   adminPass,
	}, nil
}
