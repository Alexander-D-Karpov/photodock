package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const adminCookieName = "photodock_admin"

// privateFolderIDs selects folders that are private themselves or sit under a private folder.
const privateFolderIDs = `(SELECT f.id FROM folders f JOIN folders pf ON pf.private
	AND (f.path = pf.path OR starts_with(f.path, pf.path || '/')))`

// photoVisible is the WHERE fragment for photos a viewer may see; col is the folder_id column.
func photoVisible(col string, admin bool) string {
	hidden := strings.TrimSuffix(col, "folder_id") + "hidden = false"
	if admin {
		return hidden
	}
	return hidden + " AND (" + col + " IS NULL OR " + col + " NOT IN " + privateFolderIDs + ")"
}

// folderVisible is the WHERE fragment for folders a viewer may see; col is the folder id column.
func folderVisible(col string, admin bool) string {
	if admin {
		return "TRUE"
	}
	return col + " NOT IN " + privateFolderIDs
}

func (h *Handlers) adminToken() string {
	mac := hmac.New(sha256.New, []byte(h.cfg.AdminPass))
	mac.Write([]byte("photodock-admin:" + h.cfg.AdminUser))
	return hex.EncodeToString(mac.Sum(nil))
}

func (h *Handlers) setAdminCookie(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(adminCookieName); err == nil && hmac.Equal([]byte(c.Value), []byte(h.adminToken())) {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Value:    h.adminToken(),
		Path:     "/",
		MaxAge:   30 * 24 * 3600,
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteLaxMode,
	})
}

// isAdmin accepts admin basic auth or the cookie set by adminAuth.
func (h *Handlers) isAdmin(r *http.Request) bool {
	if h.cfg.AdminPass == "" {
		return false
	}
	if user, pass, ok := r.BasicAuth(); ok {
		return hmac.Equal([]byte(user), []byte(h.cfg.AdminUser)) && hmac.Equal([]byte(pass), []byte(h.cfg.AdminPass))
	}
	c, err := r.Cookie(adminCookieName)
	return err == nil && hmac.Equal([]byte(c.Value), []byte(h.adminToken()))
}

// viewer reports admin status and keeps admin-specific responses out of shared caches.
func (h *Handlers) viewer(w http.ResponseWriter, r *http.Request) bool {
	admin := h.isAdmin(r)
	if admin {
		w.Header().Set("Cache-Control", "private, no-store")
	}
	return admin
}

func (h *Handlers) folderIsPrivate(ctx context.Context, folderID int) bool {
	var private bool
	_ = h.db.Pool().QueryRow(ctx, "SELECT $1 IN "+privateFolderIDs, folderID).Scan(&private)
	return private
}

// photoAccess reports whether the photo exists for this viewer and whether it is private.
func (h *Handlers) photoAccess(r *http.Request, id int) (ok, private bool) {
	err := h.db.Pool().QueryRow(r.Context(),
		"SELECT COALESCE(folder_id IN "+privateFolderIDs+", false) FROM photos WHERE id = $1", id).Scan(&private)
	if err != nil {
		return false, false
	}
	if private && !h.isAdmin(r) {
		return false, true
	}
	return true, private
}

func setPrivateCache(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store")
}

// privateWriter forces a no-store Cache-Control whatever the wrapped handler set.
type privateWriter struct {
	http.ResponseWriter
	done bool
}

func (pw *privateWriter) WriteHeader(code int) {
	if !pw.done {
		pw.done = true
		setPrivateCache(pw.ResponseWriter)
	}
	pw.ResponseWriter.WriteHeader(code)
}

func (pw *privateWriter) Write(b []byte) (int, error) {
	if !pw.done {
		pw.WriteHeader(http.StatusOK)
	}
	return pw.ResponseWriter.Write(b)
}

func (pw *privateWriter) Unwrap() http.ResponseWriter { return pw.ResponseWriter }

// initPrivateCache loads (or creates) the cache-name secret and moves private photos' files to keyed names.
func (h *Handlers) initPrivateCache(ctx context.Context) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		log.Printf("private cache: %v", err)
		return
	}
	_, _ = h.db.Pool().Exec(ctx, `CREATE TABLE IF NOT EXISTS app_secrets (name TEXT PRIMARY KEY, value TEXT NOT NULL)`)
	_, _ = h.db.Pool().Exec(ctx, `INSERT INTO app_secrets (name, value) VALUES ('cache_key', $1) ON CONFLICT DO NOTHING`, hex.EncodeToString(buf))
	var secret string
	if err := h.db.Pool().QueryRow(ctx, `SELECT value FROM app_secrets WHERE name = 'cache_key'`).Scan(&secret); err != nil {
		log.Printf("private cache: %v", err)
		return
	}
	h.thumbSvc.SetPrivacy(secret, func(id int) bool {
		var private bool
		_ = h.db.Pool().QueryRow(context.Background(),
			"SELECT COALESCE(folder_id IN "+privateFolderIDs+", false) FROM photos WHERE id = $1", id).Scan(&private)
		return private
	})
	h.syncPrivate(ctx)
}

// syncPrivate re-files all private photos and drops public-named OG cards of private folders.
func (h *Handlers) syncPrivate(ctx context.Context) {
	h.syncPhotosPrivacy(ctx, "folder_id IN "+privateFolderIDs)
	rows, err := h.db.Pool().Query(ctx, privateFolderIDs)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil {
			matches, _ := filepath.Glob(filepath.Join(h.cfg.CacheDir, "ogcard", fmt.Sprintf("folder_%d_*", id)))
			for _, m := range matches {
				_ = os.Remove(m)
			}
		}
	}
}

// syncPhotosPrivacy renames cached files of the photos matching where to fit their current privacy.
func (h *Handlers) syncPhotosPrivacy(ctx context.Context, where string, args ...any) {
	rows, err := h.db.Pool().Query(ctx, "SELECT id FROM photos WHERE "+where, args...)
	if err != nil {
		return
	}
	var ids []int
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		h.thumbSvc.SyncPrivacy(id)
	}
}

// syncFolderPrivacy re-files every photo in a folder subtree after its privacy changed.
func (h *Handlers) syncFolderPrivacy(ctx context.Context, folderID int) {
	h.syncPhotosPrivacy(ctx, `folder_id IN (SELECT f.id FROM folders f, folders root
		WHERE root.id = $1 AND (f.path = root.path OR starts_with(f.path, root.path || '/')))`, folderID)
	for _, pat := range []string{"folder_%d_*", "folder_%d-*"} {
		matches, _ := filepath.Glob(filepath.Join(h.cfg.CacheDir, "ogcard", fmt.Sprintf(pat, folderID)))
		for _, m := range matches {
			_ = os.Remove(m)
		}
	}
}
