package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// API tokens let scripts (e.g. a camera importer) push photos into the unsorted
// (staging) area without the admin password. Only the SHA-256 of a token is
// stored; the plain token is shown once, right after it is created.

const (
	tokenPrefix      = "pd_"
	stagingSourceAPI = "api"
	apiUploadMax     = 512 << 20 // per file; nginx client_max_body_size applies too
)

type apiToken struct {
	ID         int
	Name       string
	Prefix     string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

func newTokenSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return tokenPrefix + hex.EncodeToString(b), nil
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

// bearerToken extracts the token from "Authorization: Bearer <token>".
func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	const p = "Bearer "
	if len(auth) <= len(p) || !strings.EqualFold(auth[:len(p)], p) {
		return ""
	}
	return strings.TrimSpace(auth[len(p):])
}

// tokenAuth accepts a valid, non-revoked API token. Requests without a
// well-formed token are rejected before touching the database.
func (h *Handlers) tokenAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearerToken(r)
		if !strings.HasPrefix(tok, tokenPrefix) || len(tok) != len(tokenPrefix)+64 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="PhotoDock API"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		want := hashToken(tok)
		var id int
		var stored string
		err := h.db.Pool().QueryRow(r.Context(),
			`SELECT id, token_hash FROM api_tokens WHERE token_hash = $1 AND revoked_at IS NULL`, want).Scan(&id, &stored)
		if err != nil || subtle.ConstantTimeCompare([]byte(stored), []byte(want)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="PhotoDock API"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = h.db.Pool().Exec(r.Context(), `UPDATE api_tokens SET last_used_at = NOW() WHERE id = $1`, id)
		next(w, r)
	}
}

func (h *Handlers) listTokens(r *http.Request) ([]apiToken, error) {
	rows, err := h.db.Pool().Query(r.Context(),
		`SELECT id, name, prefix, created_at, last_used_at, revoked_at FROM api_tokens ORDER BY revoked_at IS NOT NULL, created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []apiToken
	for rows.Next() {
		var t apiToken
		if err := rows.Scan(&t.ID, &t.Name, &t.Prefix, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt); err == nil {
			out = append(out, t)
		}
	}
	return out, rows.Err()
}

func (h *Handlers) renderTokens(w http.ResponseWriter, r *http.Request, newToken, newName, errMsg string) {
	tokens, err := h.listTokens(r)
	if err != nil && errMsg == "" {
		errMsg = err.Error()
	}
	h.render(w, "admin/tokens.html", map[string]interface{}{
		"Title":    "API tokens",
		"Tokens":   tokens,
		"NewToken": newToken,
		"NewName":  newName,
		"Error":    errMsg,
		"BaseURL":  requestBaseURL(r),
	})
}

func (h *Handlers) adminTokens(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	h.renderTokens(w, r, "", "", "")
}

func (h *Handlers) adminCreateToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		h.renderTokens(w, r, "", "", "Give the token a name, e.g. \"camera importer\".")
		return
	}
	if len(name) > 100 {
		name = name[:100]
	}
	tok, err := newTokenSecret()
	if err != nil {
		h.renderTokens(w, r, "", "", err.Error())
		return
	}
	if _, err := h.db.Pool().Exec(r.Context(),
		`INSERT INTO api_tokens (name, token_hash, prefix) VALUES ($1, $2, $3)`,
		name, hashToken(tok), tok[:len(tokenPrefix)+6]); err != nil {
		h.renderTokens(w, r, "", "", err.Error())
		return
	}
	h.renderTokens(w, r, tok, name, "")
}

func (h *Handlers) adminRevokeToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_, _ = h.db.Pool().Exec(r.Context(), `UPDATE api_tokens SET revoked_at = NOW() WHERE id = $1 AND revoked_at IS NULL`, id)
	http.Redirect(w, r, "/admin/tokens", http.StatusSeeOther)
}

// apiTokenCheck: GET /api/v1/token -> 200 if the token works.
func (h *Handlers) apiTokenCheck(w http.ResponseWriter, r *http.Request) {
	h.jsonResponse(w, map[string]interface{}{"ok": true})
}

// apiStagingUpload: POST /api/v1/staging, multipart field "file".
// The file lands in the unsorted area exactly like a browser upload. A file whose
// name and size already exist in staging or in the library is skipped
// (200, "duplicate": true), so importers can safely retry.
func (h *Handlers) apiStagingUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, apiUploadMax)
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "expected multipart/form-data with a \"file\" field", http.StatusBadRequest)
		return
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			http.Error(w, "no \"file\" field", http.StatusBadRequest)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if part.FormName() != "file" || part.FileName() == "" {
			_ = part.Close()
			continue
		}
		h.storeAPIUpload(w, r, part, part.FileName())
		_ = part.Close()
		return
	}
}

func (h *Handlers) storeAPIUpload(w http.ResponseWriter, r *http.Request, src io.Reader, rawName string) {
	if !isImageFile(rawName) {
		http.Error(w, "unsupported file type (jpg, jpeg, png, arw)", http.StatusUnsupportedMediaType)
		return
	}
	filename := sanitizeFilename(rawName)
	id := stagingIDGen()
	dir := h.stagingDir(id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	dstPath := filepath.Join(dir, filename)
	dst, err := os.Create(dstPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	size, err := io.Copy(dst, src)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var dup bool
	_ = h.db.Pool().QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM staged_uploads WHERE filename = $1 AND size_bytes = $2)
		     OR EXISTS(SELECT 1 FROM photos WHERE filename = $1 AND size_bytes = $2)`, filename, size).Scan(&dup)
	if dup {
		_ = os.RemoveAll(dir)
		h.jsonResponse(w, map[string]interface{}{"duplicate": true, "filename": filename, "size": size})
		return
	}

	item, err := h.finishStaging(r.Context(), id, time.Now().Unix(), filename, dstPath, stagingSourceAPI)
	if err != nil {
		_ = os.RemoveAll(dir)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	h.jsonResponse(w, item)
}
