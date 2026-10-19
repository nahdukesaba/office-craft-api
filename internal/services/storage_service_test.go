package services

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"office-craft-api/internal/config"
)

func TestStorageUploadUsesServerCredential(t *testing.T) {
	const key = "local-test-only"

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost ||
				r.URL.Path != "/storage/v1/object/booking-proofs/user-1/booking-1/before-1.jpg" {
				t.Errorf("unexpected upload request")
			}

			if r.Header.Get("Authorization") != "Bearer "+key ||
				r.Header.Get("apikey") != key {
				t.Error("missing backend storage credential")
			}

			if r.Header.Get("Content-Type") != "image/jpeg" {
				t.Error("incorrect MIME")
			}

			w.WriteHeader(http.StatusOK)
		},
	))
	defer srv.Close()

	svc := NewStorageService(&config.Config{
		SupabaseURL:        srv.URL,
		SupabaseServiceKey: key,
	})

	err := svc.Upload(
		context.Background(),
		"booking-proofs",
		"user-1/booking-1/before-1.jpg",
		"image/jpeg",
		[]byte("test"),
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestStorageSignedURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost ||
				r.URL.Path != "/storage/v1/object/sign/booking-proofs/user-1/booking-1/before-1.jpg" {
				t.Error("unexpected signing request")
			}

			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(
				w,
				`{"signedURL":"/object/sign/booking-proofs/user-1/booking-1/before-1.jpg?token=local-test"}`,
			)
		},
	))
	defer srv.Close()

	svc := NewStorageService(&config.Config{
		SupabaseURL:        srv.URL,
		SupabaseServiceKey: "local-test-only",
	})

	signed, err := svc.SignedURL(
		context.Background(),
		"booking-proofs",
		"user-1/booking-1/before-1.jpg",
	)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(
		signed,
		srv.URL+"/storage/v1/object/sign/",
	) {
		t.Error("unexpected signed URL path")
	}
}

func TestStorageRejectsInvalidKeysAndUnconfiguredService(t *testing.T) {
	if _, err := storagePath(
		"booking-proofs", "user-1/../other.jpg",
	); err == nil {
		t.Error("path traversal was accepted")
	}

	if _, err := storagePath(
		"booking-proofs", "user-1//file.jpg",
	); err == nil {
		t.Error("empty path segment was accepted")
	}

	svc := NewStorageService(&config.Config{
		SupabaseURL: "http://127.0.0.1:1",
	})

	if err := svc.Upload(
		context.Background(),
		"booking-proofs",
		"a/b.jpg",
		"image/jpeg",
		[]byte("test"),
	); err == nil {
		t.Error("missing credential was accepted")
	}
}

func TestStorageRejectsEncodedTraversalAndOversizedPayload(t *testing.T) {
	invalidPaths := []string{
		"user-1/%2e%2e/other.jpg",
		"user-1/%2f/other.jpg",
		"/leading/slash.jpg",
		"trailing/slash/",
		"user-1/file?.jpg",
		"user-1/file#.jpg",
	}

	for _, p := range invalidPaths {
		if _, err := storagePath("booking-proofs", p); err == nil {
			t.Errorf("expected invalid path %q to be rejected", p)
		}
	}

	svc := NewStorageService(&config.Config{
		SupabaseURL:        "http://127.0.0.1:8000",
		SupabaseServiceKey: "local-test-only",
	})

	if err := svc.Upload(context.Background(), "resource-photos", "resources/empty.jpg", "image/jpeg", nil); err == nil {
		t.Error("expected empty file upload to be rejected")
	}

	oversized := make([]byte, MaxStorageUploadBytes+1)
	if err := svc.Upload(context.Background(), "resource-photos", "resources/huge.jpg", "image/jpeg", oversized); err == nil {
		t.Error("expected oversized file upload to be rejected")
	}
}

func TestStorageDeleteAndPublicURL(t *testing.T) {
	const key = "local-test-only"
	deleteCalled := false

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete ||
				r.URL.Path != "/storage/v1/object/booking-proofs/user-1/booking-1/before-1.jpg" {
				t.Errorf("unexpected delete request: %s %s", r.Method, r.URL.Path)
			}
			if r.Header.Get("Authorization") != "Bearer "+key ||
				r.Header.Get("apikey") != key {
				t.Error("missing backend storage credential on delete")
			}
			deleteCalled = true
			w.WriteHeader(http.StatusOK)
		},
	))
	defer srv.Close()

	svc := NewStorageService(&config.Config{
		SupabaseURL:        srv.URL + "/storage/v1/",
		SupabaseServiceKey: key,
	})

	if err := svc.Delete(context.Background(), "booking-proofs", "user-1/booking-1/before-1.jpg"); err != nil {
		t.Fatalf("unexpected delete error: %v", err)
	}
	if !deleteCalled {
		t.Fatal("expected delete endpoint to be called")
	}

	pubURL, err := svc.PublicURL("resource-photos", "resources/photo-1.png")
	if err != nil {
		t.Fatalf("unexpected public URL error: %v", err)
	}
	wantPub := srv.URL + "/storage/v1/object/public/resource-photos/resources/photo-1.png"
	if pubURL != wantPub {
		t.Fatalf("expected public URL %q, got %q", wantPub, pubURL)
	}
}

func TestStorageSignedURLRejectsForeignHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"signedURL":"https://malicious.example.com/storage/v1/object/sign/booking-proofs/a/b.jpg?token=bad"}`)
		},
	))
	defer srv.Close()

	svc := NewStorageService(&config.Config{
		SupabaseURL:        srv.URL,
		SupabaseServiceKey: "local-test-only",
	})

	if _, err := svc.SignedURL(context.Background(), "booking-proofs", "a/b.jpg"); err == nil {
		t.Fatal("expected foreign signed URL host to be rejected")
	}
}
