package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tyxteacat/teacat/internal/store"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("TEACAT_HOME", t.TempDir())
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	return &Server{Store: st, Version: "test", Ctx: 512}
}

func TestDashboardRoot(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "TeaCat") {
		t.Fatal("dashboard body does not mention TeaCat")
	}
}

func TestHealthAndInfo(t *testing.T) {
	s := newTestServer(t)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d", rec.Code)
	}
	var health map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if health["status"] != "ok" {
		t.Fatalf("health = %v", health)
	}

	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/info", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("info status = %d", rec.Code)
	}
	var info map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info["repo"] == "" || info["dashboard"] != "/" {
		t.Fatalf("info = %v", info)
	}
}

func TestAPIRoutesStillWork(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("version status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), s.Version) {
		t.Fatalf("version body = %q", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/tags", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("tags status = %d", rec.Code)
	}
}
