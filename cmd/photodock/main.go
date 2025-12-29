package main

import (
	"context"
	"embed"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Alexander-D-Karpov/photodock/internal/config"
	"github.com/Alexander-D-Karpov/photodock/internal/database"
	"github.com/Alexander-D-Karpov/photodock/internal/handlers"
	"github.com/Alexander-D-Karpov/photodock/internal/services"
)

//go:embed all:web
var webFS embed.FS

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	if err := os.MkdirAll(cfg.MediaRoot, 0755); err != nil {
		log.Fatalf("failed to create MEDIA_ROOT (%s): %v", cfg.MediaRoot, err)
	}
	if err := os.MkdirAll(cfg.CacheDir, 0755); err != nil {
		log.Fatalf("failed to create CACHE_DIR (%s): %v", cfg.CacheDir, err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.CacheDir, "uploads"), 0755); err != nil {
		log.Fatalf("failed to create CACHE_DIR/uploads (%s): %v", filepath.Join(cfg.CacheDir, "uploads"), err)
	}

	db, err := database.New(cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		log.Fatal(err)
	}

	thumbService := services.NewThumbnailService(cfg.MediaRoot, cfg.CacheDir)

	log.Println("Prewarming thumbnail cache...")
	thumbService.PrewarmCache()
	log.Println("Cache prewarm complete")

	exifService := services.NewExifService()
	scanService := services.NewScannerService(db, thumbService, exifService, cfg.MediaRoot)

	h := handlers.New(db, cfg, thumbService, scanService, webFS)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	server := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("Starting server on %s", cfg.ListenAddr)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatal(err)
	}
}
