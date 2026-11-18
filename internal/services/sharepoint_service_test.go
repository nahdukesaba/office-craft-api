package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"office-craft-api/internal/config"
)

type memGraphTokenStore struct {
	mu     sync.Mutex
	tokens map[string]string
}

func (m *memGraphTokenStore) GetRefreshToken(_ context.Context, id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tokens[id], nil
}

func (m *memGraphTokenStore) SaveRefreshToken(_ context.Context, id, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[id] = token
	return nil
}

func sharePointTestConfig(envRefresh string) *config.Config {
	return &config.Config{
		MSTenantID:     "tenant-1",
		MSClientID:     "client-1",
		MSClientSecret: "secret-do-not-leak",
		MSRefreshToken: envRefresh,
		SPSitePath:     "contoso.sharepoint.com:/sites/Docs",
	}
}

func newTestSharePoint(t *testing.T, srvURL, envRefresh string, store GraphTokenStore) *SharePointService {
	t.Helper()
	s := NewSharePointService(sharePointTestConfig(envRefresh), store)
	if s == nil {
		t.Fatal("expected configured SharePoint service")
	}
	s.loginBase = srvURL
	s.graphBase = srvURL + "/v1.0"
	return s
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestSharePointServiceDisabledWithoutConfig(t *testing.T) {
	cfg := sharePointTestConfig("")
	if NewSharePointService(cfg, nil) != nil {
		t.Fatal("expected nil service when refresh token is missing")
	}
}

func TestSharePointUploadDownloadDeleteAndTokenRotation(t *testing.T) {
	var (
		mu          sync.Mutex
		tokenCalls  int
		siteCalls   int
		uploadPath  string
		uploadQuery string
		uploadType  string
		deleted     bool
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if r.Method == http.MethodPost && r.URL.Path == "/tenant-1/oauth2/v2.0/token" {
			_ = r.ParseForm()
			if r.PostForm.Get("grant_type") != "refresh_token" ||
				r.PostForm.Get("client_id") != "client-1" ||
				r.PostForm.Get("client_secret") != "secret-do-not-leak" ||
				!strings.Contains(r.PostForm.Get("scope"), "Files.ReadWrite.All") {
				t.Errorf("unexpected token form: %v", r.PostForm)
			}
			switch r.PostForm.Get("refresh_token") {
			case "env-refresh", "rotated-1":
				tokenCalls++
				writeJSON(w, map[string]any{
					"access_token":  "access-1",
					"refresh_token": "rotated-1",
					"expires_in":    3600,
				})
			default:
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, map[string]string{"error": "invalid_grant"})
			}
			return
		}

		if r.Header.Get("Authorization") != "Bearer access-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1.0/sites/contoso.sharepoint.com:/sites/Docs":
			siteCalls++
			writeJSON(w, map[string]string{"id": "contoso.sharepoint.com,site-guid,web-guid"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1.0/sites/contoso.sharepoint.com,site-guid,web-guid/drive":
			writeJSON(w, map[string]string{"id": "drive-1"})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1.0/drives/drive-1/root:/booking-proofs/"):
			uploadPath = r.URL.Path
			uploadQuery = r.URL.RawQuery
			uploadType = r.Header.Get("Content-Type")
			w.WriteHeader(http.StatusCreated)
			writeJSON(w, map[string]string{
				"id":     "item-1",
				"webUrl": "https://contoso.sharepoint.com/sites/Docs/booking-proofs/x.jpg",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1.0/drives/drive-1/items/item-1":
			writeJSON(w, map[string]string{
				"id":                           "item-1",
				"@microsoft.graph.downloadUrl": "https://contoso.sharepoint.com/download.aspx?tempauth=abc",
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1.0/drives/drive-1/items/item-1":
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// A dead stored token must fall back to MS_REFRESH_TOKEN, then persist the rotated one.
	store := &memGraphTokenStore{tokens: map[string]string{sharePointTokenID: "dead-stored"}}
	s := newTestSharePoint(t, srv.URL, "env-refresh", store)
	ctx := context.Background()

	itemID, webURL, err := s.Upload(ctx, "owner-1/b-1/before-abc.jpg", "image/jpeg", []byte{0xFF, 0xD8, 0xFF})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if itemID != "drive-1/item-1" || !strings.HasPrefix(webURL, "https://contoso.sharepoint.com/") {
		t.Fatalf("unexpected upload result: %q %q", itemID, webURL)
	}
	if uploadPath != "/v1.0/drives/drive-1/root:/booking-proofs/owner-1/b-1/before-abc.jpg:/content" {
		t.Fatalf("unexpected upload path: %s", uploadPath)
	}
	if uploadQuery != "@microsoft.graph.conflictBehavior=fail" || uploadType != "image/jpeg" {
		t.Fatalf("unexpected upload query/type: %q %q", uploadQuery, uploadType)
	}
	if got, _ := store.GetRefreshToken(ctx, sharePointTokenID); got != "rotated-1" {
		t.Fatalf("expected rotated refresh token to be saved, got %q", got)
	}

	if _, _, err := s.Upload(ctx, "owner-1/b-1/after-def.jpg", "image/jpeg", []byte{0xFF, 0xD8, 0xFF}); err != nil {
		t.Fatalf("second upload: %v", err)
	}

	u, err := s.DownloadURL(ctx, itemID)
	if err != nil || u != "https://contoso.sharepoint.com/download.aspx?tempauth=abc" {
		t.Fatalf("unexpected download URL: %q %v", u, err)
	}
	// Bare item ids (no drive prefix) resolve against the current drive.
	if err := s.Delete(ctx, "item-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !deleted {
		t.Fatal("expected delete to reach Graph")
	}
	if tokenCalls != 1 {
		t.Fatalf("expected access token to be cached (1 refresh), got %d", tokenCalls)
	}
	if siteCalls != 1 {
		t.Fatalf("expected drive id to be cached (1 site lookup), got %d", siteCalls)
	}
}

func TestSharePointRejectsBadInputAndDoesNotLeakSecrets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]string{"error": "invalid_grant", "error_description": "secret-do-not-leak dead-env"})
	}))
	defer srv.Close()

	s := newTestSharePoint(t, srv.URL, "dead-env", &memGraphTokenStore{tokens: map[string]string{}})
	ctx := context.Background()

	_, _, err := s.Upload(ctx, "owner-1/b-1/before-abc.jpg", "image/jpeg", []byte{0xFF, 0xD8, 0xFF})
	if err == nil {
		t.Fatal("expected upload to fail with rejected refresh token")
	}
	if strings.Contains(err.Error(), "secret-do-not-leak") || strings.Contains(err.Error(), "dead-env") {
		t.Fatalf("error leaked credentials: %v", err)
	}

	if _, _, err := s.Upload(ctx, "../escape.jpg", "image/jpeg", []byte{1}); err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
	if _, _, err := s.Upload(ctx, "a/b.jpg", "image/jpeg", nil); err == nil {
		t.Fatal("expected empty file to be rejected")
	}
	if _, err := s.DownloadURL(ctx, "../item"); err == nil {
		t.Fatal("expected invalid item id to be rejected")
	}
}

func TestOneDriveModeUsesMeDriveAndOwnTokenSlot(t *testing.T) {
	var (
		mu         sync.Mutex
		meCalls    int
		itemTarget string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/tenant-1/oauth2/v2.0/token" {
			_ = r.ParseForm()
			if r.PostForm.Get("scope") != "offline_access Files.ReadWrite" {
				t.Errorf("unexpected OneDrive scope: %q", r.PostForm.Get("scope"))
			}
			writeJSON(w, map[string]any{"access_token": "access-1", "refresh_token": "od-rotated", "expires_in": 3600})
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1.0/me/drive":
			meCalls++
			writeJSON(w, map[string]string{"id": "od-drive"})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1.0/drives/od-drive/root:/booking-proofs/"):
			w.WriteHeader(http.StatusCreated)
			writeJSON(w, map[string]string{"id": "item-9", "webUrl": "https://kemenpkp-my.sharepoint.com/x.jpg"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1.0/drives/"):
			itemTarget = r.URL.Path
			writeJSON(w, map[string]string{"@microsoft.graph.downloadUrl": "https://kemenpkp-my.sharepoint.com/download.aspx?tempauth=x"})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := sharePointTestConfig("env-refresh")
	cfg.SPSitePath = ""
	store := &memGraphTokenStore{tokens: map[string]string{}}
	s := NewSharePointService(cfg, store)
	if s == nil {
		t.Fatal("expected OneDrive mode when SP_SITE_PATH is empty")
	}
	s.loginBase, s.graphBase = srv.URL, srv.URL+"/v1.0"
	ctx := context.Background()

	ref, _, err := s.Upload(ctx, "owner-1/b-1/before-abc.jpg", "image/jpeg", []byte{0xFF})
	if err != nil || ref != "od-drive/item-9" {
		t.Fatalf("unexpected upload result: %q %v", ref, err)
	}
	if got, _ := store.GetRefreshToken(ctx, oneDriveTokenID); got != "od-rotated" {
		t.Fatalf("expected OneDrive token slot to hold rotated token, got %q", got)
	}
	if got, _ := store.GetRefreshToken(ctx, sharePointTokenID); got != "" {
		t.Fatalf("SharePoint token slot must stay untouched, got %q", got)
	}

	// A ref from another drive (e.g. made before a plan switch) is addressed directly.
	if _, err := s.DownloadURL(ctx, "other-drive/item-3"); err != nil {
		t.Fatalf("download: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if itemTarget != "/v1.0/drives/other-drive/items/item-3" || meCalls != 1 {
		t.Fatalf("unexpected item target %q or /me/drive calls %d", itemTarget, meCalls)
	}
	if _, err := s.DownloadURL(ctx, "../x/item"); err == nil {
		t.Fatal("expected invalid drive ref to be rejected")
	}
}
