package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBearerToken(t *testing.T) {
	cases := map[string]string{
		"":                 "",
		"Basic abc":        "",
		"Bearer ":          "",
		"Bearer pd_abc":    "pd_abc",
		"bearer  pd_abc  ": "pd_abc",
	}
	for in, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if in != "" {
			r.Header.Set("Authorization", in)
		}
		if got := bearerToken(r); got != want {
			t.Errorf("bearerToken(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestNewTokenSecret(t *testing.T) {
	a, err := newTokenSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newTokenSecret()
	if a == b || !strings.HasPrefix(a, tokenPrefix) || len(a) != len(tokenPrefix)+64 {
		t.Fatalf("bad token %q", a)
	}
	if hashToken(a) == a || len(hashToken(a)) != 64 {
		t.Fatal("hashToken must return a hex sha256, never the token")
	}
}

// Malformed or missing tokens are rejected before any DB access (h.db is nil, so a query would panic).
func TestTokenAuthRejectsWithoutDB(t *testing.T) {
	h := &Handlers{}
	called := false
	handler := h.tokenAuth(func(w http.ResponseWriter, r *http.Request) { called = true })
	for _, auth := range []string{"", "Bearer nope", "Bearer pd_short", "Basic dXNlcjpwYXNz"} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/staging", nil)
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != http.StatusUnauthorized || called {
			t.Errorf("auth %q: status %d, called=%v; want 401 and not called", auth, w.Code, called)
		}
	}
}
