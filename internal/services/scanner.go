package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/Alexander-D-Karpov/photodock/internal/database"
)

type ScannerService struct {
	db        *database.DB
	thumbSvc  *ThumbnailService
	exifSvc   *ExifService
	mediaRoot string
}

func NewScannerService(db *database.DB, thumbSvc *ThumbnailService, exifSvc *ExifService, mediaRoot string) *ScannerService {
	return &ScannerService{db: db, thumbSvc: thumbSvc, exifSvc: exifSvc, mediaRoot: mediaRoot}
}

func (s *ScannerService) ScanAll(ctx context.Context) error {
	return s.scanDir(ctx, "", nil)
}

func (s *ScannerService) ScanFolder(ctx context.Context, folderPath string) error {
	var folderID *int
	if folderPath != "" {
		var id int
		err := s.db.Pool().QueryRow(ctx, "SELECT id FROM folders WHERE path = $1", folderPath).Scan(&id)
		if err != nil {
			return err
		}
		folderID = &id
	}
	return s.scanDir(ctx, folderPath, folderID)
}

func (s *ScannerService) scanDir(ctx context.Context, relPath string, currentFolderID *int) error {
	absPath := filepath.Join(s.mediaRoot, relPath)

	entries, err := os.ReadDir(absPath)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		entryRelPath := filepath.Join(relPath, entry.Name())

		if entry.IsDir() {
			childFolderID, err := s.ensureFolder(ctx, entryRelPath, entry.Name(), currentFolderID)
			if err != nil {
				log.Printf("ensure folder error %s: %v", entryRelPath, err)
				continue
			}
			if err := s.scanDir(ctx, entryRelPath, &childFolderID); err != nil {
				log.Printf("scan dir error %s: %v", entryRelPath, err)
			}
		} else if isImageFile(entry.Name()) {
			if err := s.processPhoto(ctx, entryRelPath, currentFolderID); err != nil {
				log.Printf("process photo error %s: %v", entryRelPath, err)
			}
		}
	}

	return nil
}

func (s *ScannerService) ensureFolder(ctx context.Context, path, name string, parentID *int) (int, error) {
	var id int
	err := s.db.Pool().QueryRow(ctx, "SELECT id FROM folders WHERE path = $1", path).Scan(&id)
	if err == nil {
		return id, nil
	}

	err = s.db.Pool().QueryRow(ctx,
		`INSERT INTO folders (parent_id, name, path) VALUES ($1, $2, $3) 
		ON CONFLICT (path) DO UPDATE SET name = EXCLUDED.name 
		RETURNING id`,
		parentID, name, path).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("ensureFolder %q: %w", path, err)
	}
	return id, nil
}

func (s *ScannerService) processPhoto(ctx context.Context, relPath string, folderID *int) error {
	var exists bool
	err := s.db.Pool().QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM photos WHERE path = $1)", relPath).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check exists: %w", err)
	}
	if exists {
		return nil
	}

	if folderID != nil {
		var folderExists bool
		err := s.db.Pool().QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM folders WHERE id = $1)", *folderID).Scan(&folderExists)
		if err != nil || !folderExists {
			log.Printf("folder_id %d does not exist for %s, setting to NULL", *folderID, relPath)
			folderID = nil
		}
	}

	absPath := filepath.Join(s.mediaRoot, relPath)
	info, err := os.Stat(absPath)
	if err != nil {
		return err
	}

	if err := s.exifSvc.StripGPS(absPath); err != nil {
		log.Printf("strip GPS error %s: %v", relPath, err)
	}

	exifInfo, takenAt, _ := s.exifSvc.Extract(absPath)
	width, height, _ := s.thumbSvc.GetImageDimensions(relPath)
	blurhash, _ := s.thumbSvc.GenerateBlurhash(relPath)

	var exifJSON []byte
	if exifInfo != nil {
		exifJSON, _ = json.Marshal(exifInfo)
	}

	var takenAtPtr *time.Time
	if !takenAt.IsZero() {
		takenAtPtr = &takenAt
	}

	for attempt := 0; attempt < 5; attempt++ {
		urlPath := s.generateURLPath(ctx, relPath)

		var photoID int
		err = s.db.Pool().QueryRow(ctx,
			`INSERT INTO photos (folder_id, filename, path, url_path, width, height, size_bytes, blurhash, exif_data, taken_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (path) DO NOTHING
			RETURNING id`,
			folderID, filepath.Base(relPath), relPath, urlPath, width, height, info.Size(), blurhash, exifJSON, takenAtPtr).Scan(&photoID)

		if err != nil && strings.Contains(err.Error(), "no rows") {
			return nil
		}

		if err == nil {
			_, _ = s.thumbSvc.GetThumbnailPathByID(photoID, relPath, "small")
			_, _ = s.thumbSvc.GetThumbnailPathByID(photoID, relPath, "medium")
			_, _ = s.thumbSvc.GetThumbnailPathByID(photoID, relPath, "large")
			return nil
		}

		if !strings.Contains(err.Error(), "photos_url_path_key") {
			return fmt.Errorf("insert photo %s: %w", relPath, err)
		}

		log.Printf("url_path collision for %s (attempt %d), retrying", relPath, attempt+1)
	}

	return fmt.Errorf("failed to insert photo %s after retries: %w", relPath, err)
}

func (s *ScannerService) ReprocessAllMetadata(ctx context.Context) error {
	rows, err := s.db.Pool().Query(ctx, "SELECT id, path FROM photos ORDER BY id")
	if err != nil {
		return err
	}
	defer rows.Close()

	type photoRow struct {
		id   int
		path string
	}
	var photos []photoRow
	for rows.Next() {
		var p photoRow
		if err := rows.Scan(&p.id, &p.path); err != nil {
			continue
		}
		photos = append(photos, p)
	}
	rows.Close()

	log.Printf("Reprocessing metadata for %d photos", len(photos))

	for i, p := range photos {
		absPath := filepath.Join(s.mediaRoot, p.path)
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			log.Printf("skip missing file: %s", p.path)
			continue
		}

		exifInfo, takenAt, _ := s.exifSvc.Extract(absPath)
		width, height, _ := s.thumbSvc.GetImageDimensions(p.path)

		var exifJSON []byte
		if exifInfo != nil {
			exifJSON, _ = json.Marshal(exifInfo)
		}

		var takenAtPtr *time.Time
		if !takenAt.IsZero() {
			takenAtPtr = &takenAt
		}

		blurhash, _ := s.thumbSvc.GenerateBlurhash(p.path)

		_, err := s.db.Pool().Exec(ctx,
			`UPDATE photos SET 
				width = $1, height = $2, exif_data = $3, taken_at = COALESCE($4, taken_at),
				blurhash = COALESCE($5, blurhash), updated_at = NOW()
			WHERE id = $6`,
			width, height, exifJSON, takenAtPtr, blurhash, p.id)

		if err != nil {
			log.Printf("reprocess error photo %d (%s): %v", p.id, p.path, err)
		}

		if (i+1)%100 == 0 {
			log.Printf("Reprocessed %d/%d photos", i+1, len(photos))
		}
	}

	log.Printf("Metadata reprocessing complete")
	return nil
}

func (s *ScannerService) generateURLPath(ctx context.Context, filePath string) string {
	urlPath := sanitizeURLPath(filePath)

	var exists bool
	_ = s.db.Pool().QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM photos WHERE url_path = $1)", urlPath).Scan(&exists)
	if !exists {
		return urlPath
	}

	ext := filepath.Ext(urlPath)
	base := strings.TrimSuffix(urlPath, ext)

	for i := 1; i < 100; i++ {
		candidate := fmt.Sprintf("%s-%d%s", base, i, ext)
		_ = s.db.Pool().QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM photos WHERE url_path = $1)", candidate).Scan(&exists)
		if !exists {
			return candidate
		}
	}

	return fmt.Sprintf("%s-%s%s", base, randHex(4), ext)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func sanitizeURLPath(path string) string {
	path = strings.ToLower(path)

	var result strings.Builder
	prevDash := false

	for _, r := range path {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			result.WriteRune(r)
			prevDash = false
		} else if r == '/' || r == '.' || r == '-' || r == '_' {
			result.WriteRune(r)
			prevDash = (r == '-')
		} else if r == ' ' {
			if !prevDash {
				result.WriteRune('-')
				prevDash = true
			}
		}
	}

	urlPath := result.String()
	re := regexp.MustCompile(`-+`)
	urlPath = re.ReplaceAllString(urlPath, "-")
	urlPath = strings.Trim(urlPath, "-")

	parts := strings.Split(urlPath, "/")
	for i, part := range parts {
		parts[i] = strings.Trim(part, "-")
	}
	urlPath = strings.Join(parts, "/")

	return urlPath
}

func (s *ScannerService) CleanOrphans(ctx context.Context) error {
	rows, err := s.db.Pool().Query(ctx, "SELECT id, path FROM photos")
	if err != nil {
		return err
	}
	defer rows.Close()

	var orphanIDs []int
	for rows.Next() {
		var id int
		var path string
		if err := rows.Scan(&id, &path); err != nil {
			continue
		}
		absPath := filepath.Join(s.mediaRoot, path)
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			orphanIDs = append(orphanIDs, id)
		}
	}

	for _, id := range orphanIDs {
		_, _ = s.db.Pool().Exec(ctx, "DELETE FROM photos WHERE id = $1", id)
	}

	_, err = s.db.Pool().Exec(ctx, `
		DELETE FROM folders WHERE id IN (
			SELECT f.id FROM folders f 
			LEFT JOIN photos p ON p.folder_id = f.id 
			LEFT JOIN folders sf ON sf.parent_id = f.id 
			WHERE p.id IS NULL AND sf.id IS NULL
		)
	`)

	return err
}

func (s *ScannerService) RegenerateURLPaths(ctx context.Context) error {
	rows, err := s.db.Pool().Query(ctx, "SELECT id, path FROM photos ORDER BY id")
	if err != nil {
		return err
	}
	defer rows.Close()

	type photoRow struct {
		id   int
		path string
	}
	var photos []photoRow
	for rows.Next() {
		var p photoRow
		if err := rows.Scan(&p.id, &p.path); err != nil {
			continue
		}
		photos = append(photos, p)
	}
	rows.Close()

	_, err = s.db.Pool().Exec(ctx, "UPDATE photos SET url_path = NULL")
	if err != nil {
		return err
	}

	for _, p := range photos {
		urlPath := s.generateURLPath(ctx, p.path)
		_, _ = s.db.Pool().Exec(ctx, "UPDATE photos SET url_path = $1 WHERE id = $2", urlPath, p.id)
	}

	return nil
}

func isImageFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".jpg" || ext == ".jpeg" || ext == ".png"
}
