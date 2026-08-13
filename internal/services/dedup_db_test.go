package services

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Alexander-D-Karpov/photodock/internal/database"
)

func TestDedupPhotosDB(t *testing.T) {
	dsn := os.Getenv("PHOTODOCK_TEST_DB")
	if dsn == "" {
		t.Skip("set PHOTODOCK_TEST_DB to a throwaway Postgres to run this test")
	}

	db, err := database.New(dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	media := t.TempDir()
	cache := t.TempDir()

	dup := make([]byte, 1000)
	for i := range dup {
		dup[i] = 'a'
	}
	diff := make([]byte, 1000)
	copy(diff, dup)
	diff[999] = 'b'

	files := map[string][]byte{
		"dedup_A.jpg": dup,
		"dedup_B.jpg": dup,
		"dedup_C.jpg": diff,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(media, name), content, 0644); err != nil {
			t.Fatal(err)
		}
	}

	t.Cleanup(func() {
		_, _ = db.Pool().Exec(ctx, "DELETE FROM photos WHERE path LIKE 'dedup_%'")
	})
	_, _ = db.Pool().Exec(ctx, "DELETE FROM photos WHERE path LIKE 'dedup_%'")

	for _, name := range []string{"dedup_A.jpg", "dedup_B.jpg", "dedup_C.jpg"} {
		_, err := db.Pool().Exec(ctx,
			`INSERT INTO photos (filename, path, size_bytes) VALUES ($1, $1, 1000)`, name)
		if err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}
	}

	thumb := NewThumbnailService(media, cache)
	svc := NewScannerService(db, thumb, NewExifService(), media)

	if err := svc.DedupPhotos(ctx); err != nil {
		t.Fatalf("DedupPhotos: %v", err)
	}

	remaining := map[string]bool{}
	rows, err := db.Pool().Query(ctx, "SELECT path FROM photos WHERE path LIKE 'dedup_%'")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err == nil {
			remaining[p] = true
		}
	}
	rows.Close()

	if len(remaining) != 2 {
		t.Fatalf("expected 2 photos after dedup, got %d: %v", len(remaining), remaining)
	}
	if remaining["dedup_B.jpg"] {
		t.Error("dedup_B.jpg (duplicate of A) should have been removed")
	}
	if !remaining["dedup_A.jpg"] || !remaining["dedup_C.jpg"] {
		t.Error("dedup_A.jpg (first) and dedup_C.jpg (unique) should remain")
	}
	if _, err := os.Stat(filepath.Join(media, "dedup_B.jpg")); !os.IsNotExist(err) {
		t.Error("dedup_B.jpg file should have been deleted from disk")
	}
}
