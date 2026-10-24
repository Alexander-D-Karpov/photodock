package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	DatabaseURL           string
	MediaRoot             string
	CacheDir              string
	ListenAddr            string
	AdminUser             string
	AdminPass             string
	DevelopProxySize      int
	DevelopPreviewWorkers int
	LensfunDir            string
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
		DatabaseURL:           dbURL,
		MediaRoot:             mediaRootAbs,
		CacheDir:              cacheDirAbs,
		ListenAddr:            listenAddr,
		AdminUser:             adminUser,
		AdminPass:             adminPass,
		DevelopProxySize:      envInt("DEVELOP_PROXY_SIZE", 2048),
		DevelopPreviewWorkers: envInt("DEVELOP_PREVIEW_WORKERS", 2),
		LensfunDir:            os.Getenv("LENSFUN_DB"),
	}, nil
}

func envInt(key string, def int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil || v <= 0 {
		return def
	}
	return v
}
