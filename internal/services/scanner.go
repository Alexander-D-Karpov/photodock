package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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

// orientedDimensions swaps width/height for EXIF orientation 5–8; not for already auto-oriented dimensions.
func orientedDimensions(width, height, orientation int) (int, int) {
	if orientation >= 5 && orientation <= 8 {
		return height, width
	}
	return width, height
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
			if err := s.processPhoto(ctx, entryRelPath, currentFolderID, 0); err != nil {
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

func (s *ScannerService) processPhoto(ctx context.Context, relPath string, folderID *int, rotation int) error {
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

	rotation = NormalizeRotation(rotation)

	absPath := filepath.Join(s.mediaRoot, relPath)
	info, err := os.Stat(absPath)
	if err != nil {
		return err
	}

	if err := s.exifSvc.StripGPS(absPath); err != nil {
		log.Printf("strip GPS error %s: %v", relPath, err)
	}

	exifInfo, takenAt, _ := s.exifSvc.Extract(absPath)
	width, height, _ := s.thumbSvc.GetImageDimensions(relPath, rotation)
	blurhash, _ := s.thumbSvc.GenerateBlurhash(relPath, rotation)

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
			`INSERT INTO photos (folder_id, filename, path, url_path, width, height, size_bytes, blurhash, exif_data, taken_at, rotation)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (path) DO NOTHING
			RETURNING id`,
			folderID, filepath.Base(relPath), relPath, urlPath, width, height, info.Size(), blurhash, exifJSON, takenAtPtr, rotation).Scan(&photoID)

		if err != nil && strings.Contains(err.Error(), "no rows") {
			return nil
		}

		if err == nil {
			s.thumbSvc.RebuildDerivatives(photoID, relPath, blurhash, rotation, width, height)
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
	rows, err := s.db.Pool().Query(ctx, `
		SELECT p.id, p.path, COALESCE(p.rotation, 0), (e.photo_id IS NOT NULL)
		FROM photos p LEFT JOIN photo_edits e ON e.photo_id = p.id ORDER BY p.id`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type photoRow struct {
		id       int
		path     string
		rotation int
		edited   bool
	}
	var photos []photoRow
	for rows.Next() {
		var p photoRow
		if err := rows.Scan(&p.id, &p.path, &p.rotation, &p.edited); err != nil {
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

		var exifJSON []byte
		if exifInfo != nil {
			exifJSON, _ = json.Marshal(exifInfo)
		}

		var takenAtPtr *time.Time
		if !takenAt.IsZero() {
			takenAtPtr = &takenAt
		}

		// Edited photos keep width/height/blurhash from the develop render; recomputing would revert it.
		if p.edited {
			if _, err := s.db.Pool().Exec(ctx,
				`UPDATE photos SET exif_data = $1, taken_at = COALESCE($2, taken_at), updated_at = NOW() WHERE id = $3`,
				exifJSON, takenAtPtr, p.id); err != nil {
				log.Printf("reprocess error photo %d (%s): %v", p.id, p.path, err)
			}
			continue
		}

		rotation := NormalizeRotation(p.rotation)
		width, height, _ := s.thumbSvc.GetImageDimensions(p.path, rotation)
		blurhash, _ := s.thumbSvc.GenerateBlurhash(p.path, rotation)

		_, err := s.db.Pool().Exec(ctx,
			`UPDATE photos SET 
				width = $1, height = $2, exif_data = $3, taken_at = COALESCE($4, taken_at),
				blurhash = COALESCE($5, blurhash), updated_at = NOW()
			WHERE id = $6`,
			width, height, exifJSON, takenAtPtr, blurhash, p.id)

		if err != nil {
			log.Printf("reprocess error photo %d (%s): %v", p.id, p.path, err)
			continue
		}

		s.thumbSvc.RebuildDerivatives(p.id, p.path, blurhash, rotation, width, height)

		if (i+1)%100 == 0 {
			log.Printf("Reprocessed %d/%d photos", i+1, len(photos))
		}
	}

	log.Printf("Metadata reprocessing complete")
	return nil
}

func (s *ScannerService) generateURLPath(ctx context.Context, filePath string) string {
	base := strings.TrimSuffix(filePath, filepath.Ext(filePath))
	urlPath := sanitizeURLPath(base)

	taken := func(candidate string) bool {
		var exists bool
		_ = s.db.Pool().QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM photos WHERE url_path = $1)", candidate).Scan(&exists)
		if exists {
			return true
		}
		_ = s.db.Pool().QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM folders WHERE path = $1)", candidate).Scan(&exists)
		return exists
	}

	if !taken(urlPath) {
		return urlPath
	}

	for i := 1; i < 100; i++ {
		candidate := fmt.Sprintf("%s-%d", urlPath, i)
		if !taken(candidate) {
			return candidate
		}
	}

	return fmt.Sprintf("%s-%s", urlPath, randHex(4))
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
	return ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".arw"
}

func (s *ScannerService) ImportFile(ctx context.Context, relPath string, folderID *int, rotation int) error {
	return s.processPhoto(ctx, relPath, folderID, rotation)
}

func (s *ScannerService) DedupPhotos(ctx context.Context) error {
	rows, err := s.db.Pool().Query(ctx,
		"SELECT size_bytes FROM photos GROUP BY size_bytes HAVING COUNT(*) > 1")
	if err != nil {
		return err
	}
	var sizes []int64
	for rows.Next() {
		var sz int64
		if err := rows.Scan(&sz); err == nil {
			sizes = append(sizes, sz)
		}
	}
	rows.Close()

	for _, sz := range sizes {
		grp, err := s.db.Pool().Query(ctx, "SELECT id, path FROM photos WHERE size_bytes = $1 ORDER BY id", sz)
		if err != nil {
			continue
		}
		type row struct {
			id   int
			path string
		}
		var items []row
		for grp.Next() {
			var r row
			if err := grp.Scan(&r.id, &r.path); err == nil {
				items = append(items, r)
			}
		}
		grp.Close()

		seen := make(map[string]int)
		for _, it := range items {
			sum, err := fileHash(filepath.Join(s.mediaRoot, it.path))
			if err != nil {
				continue
			}
			if _, ok := seen[sum]; ok {
				_, _ = s.db.Pool().Exec(ctx, "DELETE FROM photos WHERE id = $1", it.id)
				_ = s.thumbSvc.DeleteThumbnailsByID(it.id)
				_ = os.RemoveAll(s.thumbSvc.DevelopDir(it.id))
				_ = os.Remove(filepath.Join(s.mediaRoot, it.path))
				log.Printf("dedup removed duplicate photo %d (%s)", it.id, it.path)
			} else {
				seen[sum] = it.id
			}
		}
	}
	return nil
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
