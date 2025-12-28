package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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

func (s *ScannerService) scanDir(ctx context.Context, relPath string, parentID *int) error {
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
			folderID, err := s.ensureFolder(ctx, entryRelPath, entry.Name(), parentID)
			if err != nil {
				continue
			}
			s.scanDir(ctx, entryRelPath, &folderID)
		} else if isImageFile(entry.Name()) {
			s.processPhoto(ctx, entryRelPath, parentID)
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
	return id, err
}

func (s *ScannerService) processPhoto(ctx context.Context, relPath string, folderID *int) error {
	var exists bool
	err := s.db.Pool().QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM photos WHERE path = $1)", relPath).Scan(&exists)
	if err != nil || exists {
		return err
	}

	absPath := filepath.Join(s.mediaRoot, relPath)
	info, err := os.Stat(absPath)
	if err != nil {
		return err
	}

	if err := s.exifSvc.StripGPS(absPath); err != nil {
	}

	exifInfo, takenAt, _ := s.exifSvc.Extract(absPath)
	width, height, _ := s.thumbSvc.GetImageDimensions(relPath)
	blurhash, _ := s.thumbSvc.GenerateBlurhash(relPath)

	s.thumbSvc.GetThumbnailPath(relPath, "small")
	s.thumbSvc.GetThumbnailPath(relPath, "medium")

	var exifJSON []byte
	if exifInfo != nil {
		exifJSON, _ = json.Marshal(exifInfo)
	}

	var takenAtPtr *time.Time
	if !takenAt.IsZero() {
		takenAtPtr = &takenAt
	}

	urlPath := s.generateURLPath(ctx, relPath)

	_, err = s.db.Pool().Exec(ctx,
		`INSERT INTO photos (folder_id, filename, path, url_path, width, height, size_bytes, blurhash, exif_data, taken_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (path) DO UPDATE SET url_path = COALESCE(photos.url_path, EXCLUDED.url_path)`,
		folderID, filepath.Base(relPath), relPath, urlPath, width, height, info.Size(), blurhash, exifJSON, takenAtPtr)

	return err
}

func (s *ScannerService) generateURLPath(ctx context.Context, filePath string) string {
	urlPath := sanitizeURLPath(filePath)

	var exists bool
	err := s.db.Pool().QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM photos WHERE url_path = $1)", urlPath).Scan(&exists)
	if err != nil || !exists {
		return urlPath
	}

	ext := filepath.Ext(urlPath)
	base := strings.TrimSuffix(urlPath, ext)

	for i := 1; i < 10000; i++ {
		candidate := fmt.Sprintf("%s-%d%s", base, i, ext)
		err := s.db.Pool().QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM photos WHERE url_path = $1)", candidate).Scan(&exists)
		if err != nil || !exists {
			return candidate
		}
	}

	return fmt.Sprintf("%s-%d%s", base, time.Now().UnixNano(), ext)
}

func sanitizeURLPath(path string) string {
	path = strings.ToLower(path)

	path = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '/' || r == '.' || r == '-' || r == '_' {
			return r
		}
		if r == ' ' {
			return '-'
		}
		return -1
	}, path)

	re := regexp.MustCompile(`-+`)
	path = re.ReplaceAllString(path, "-")

	path = strings.Trim(path, "-")

	parts := strings.Split(path, "/")
	for i, part := range parts {
		parts[i] = strings.Trim(part, "-")
	}
	path = strings.Join(parts, "/")

	return path
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
		s.db.Pool().Exec(ctx, "DELETE FROM photos WHERE id = $1", id)
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
		rows.Scan(&p.id, &p.path)
		photos = append(photos, p)
	}
	rows.Close()

	_, err = s.db.Pool().Exec(ctx, "UPDATE photos SET url_path = NULL")
	if err != nil {
		return err
	}

	for _, p := range photos {
		urlPath := s.generateURLPath(ctx, p.path)
		s.db.Pool().Exec(ctx, "UPDATE photos SET url_path = $1 WHERE id = $2", urlPath, p.id)
	}

	return nil
}

func isImageFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".jpg" || ext == ".jpeg" || ext == ".png"
}
