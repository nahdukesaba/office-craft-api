package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/config"
	"office-craft-api/internal/middleware"
	"office-craft-api/internal/models"
	"office-craft-api/internal/services"
)

var (
	validJPEGSample = []byte{
		0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10,
		'J', 'F', 'I', 'F', 0x00, 0x01, 0x01,
		0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00,
		0xFF, 0xD9,
	}
	validPNGSample = []byte{
		0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n',
		0x00, 0x00, 0x00, 0x0D, 'I', 'H', 'D', 'R',
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE,
	}
)

type fakeBookingFinder struct {
	mu       sync.Mutex
	bookings map[string]*models.Booking
	err      error
}

func (f *fakeBookingFinder) GetByID(_ context.Context, id string) (*models.Booking, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	b, ok := f.bookings[id]
	if !ok {
		return nil, nil
	}
	cp := *b
	return &cp, nil
}

type fakeProofStore struct {
	mu         sync.Mutex
	proofs     map[string][]models.BookingProof
	createErr  error
	listErr    error
	createdSeq int
}

func (s *fakeProofStore) Create(_ context.Context, bookingID, kind, path, uploadedBy string) (*models.BookingProof, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.createErr != nil {
		return nil, s.createErr
	}
	s.createdSeq++
	p := models.BookingProof{
		ID:         "proof-test-id",
		BookingID:  bookingID,
		Kind:       kind,
		Path:       path,
		UploadedBy: uploadedBy,
		CreatedAt:  time.Now().UTC(),
	}
	if s.proofs == nil {
		s.proofs = make(map[string][]models.BookingProof)
	}
	s.proofs[bookingID] = append(s.proofs[bookingID], p)
	return &p, nil
}

func (s *fakeProofStore) ListByBooking(_ context.Context, bookingID string) ([]models.BookingProof, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listErr != nil {
		return nil, s.listErr
	}
	rows := s.proofs[bookingID]
	out := make([]models.BookingProof, len(rows))
	copy(out, rows)
	return out, nil
}

func newHandlerTestApp() *fiber.App {
	return fiber.New(fiber.Config{
		BodyLimit: 11 * 1024 * 1024,
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			var appErr *apperror.AppError
			if errors.As(err, &appErr) {
				return c.Status(appErr.Status).JSON(appErr)
			}
			var fiberErr *fiber.Error
			if errors.As(err, &fiberErr) {
				return c.Status(fiberErr.Code).JSON(fiber.Map{
					"code":    "FIBER_ERROR",
					"message": fiberErr.Message,
				})
			}
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"code":    "INTERNAL_ERROR",
				"message": "unexpected error",
			})
		},
	})
}

func testAuthMiddleware(c *fiber.Ctx) error {
	userID := strings.TrimSpace(c.Get("X-Test-User-Id"))
	role := strings.TrimSpace(c.Get("X-Test-Role"))
	if userID == "" {
		return apperror.Unauthorized("missing or malformed Authorization header")
	}
	if role == "" {
		role = models.RoleUser
	}
	c.Locals(middleware.LocalsUserID, userID)
	c.Locals(middleware.LocalsRole, role)
	return c.Next()
}

func makeMultipartRequest(
	t *testing.T,
	method, target string,
	fields map[string]string,
	fileField, fileName string,
	fileContent []byte,
) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	for k, v := range fields {
		if err := writer.WriteField(k, v); err != nil {
			t.Fatalf("WriteField(%s): %v", k, err)
		}
	}

	if fileField != "" {
		part, err := writer.CreateFormFile(fileField, fileName)
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		if _, err := part.Write(fileContent); err != nil {
			t.Fatalf("Write file part: %v", err)
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("Close multipart writer: %v", err)
	}

	req := httptest.NewRequest(method, target, &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func TestStorageHandler_ResourcePhotoUpload_RBACAndValidation(t *testing.T) {
	var uploadedObjects []string
	storageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-service-role-key" {
			t.Errorf("unexpected storage auth header: %s", r.Header.Get("Authorization"))
		}
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/storage/v1/object/resource-photos/resources/") {
			uploadedObjects = append(uploadedObjects, r.URL.Path)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"Key":"ok"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer storageServer.Close()

	cfg := &config.Config{
		SupabaseURL:                    storageServer.URL,
		SupabaseServiceKey:         "test-service-role-key",
		SupabaseStorageBucketResources: "resource-photos",
		SupabaseStorageBucketProofs:    "booking-proofs",
	}

	bookings := &fakeBookingFinder{bookings: map[string]*models.Booking{}}
	proofs := &fakeProofStore{}
	proofSvc := services.NewProofService(bookings)
	handler := NewStorageHandler(cfg, bookings, proofs, proofSvc)

	app := newHandlerTestApp()
	app.Post(
		"/api/resources/photos/upload",
		testAuthMiddleware,
		middleware.RequireAdmin(),
		handler.UploadResourcePhoto,
	)

	// 1. Unauthenticated user -> 401
	reqUnauth := makeMultipartRequest(t, http.MethodPost, "/api/resources/photos/upload", nil, "file", "room.jpg", validJPEGSample)
	respUnauth, err := app.Test(reqUnauth, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer respUnauth.Body.Close()
	if respUnauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated request, got %d", respUnauth.StatusCode)
	}

	// 2. Non-admin user -> 403 FORBIDDEN
	reqUser := makeMultipartRequest(t, http.MethodPost, "/api/resources/photos/upload", nil, "file", "room.jpg", validJPEGSample)
	reqUser.Header.Set("X-Test-User-Id", "user-1")
	reqUser.Header.Set("X-Test-Role", models.RoleUser)
	respUser, err := app.Test(reqUser, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer respUser.Body.Close()
	if respUser.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin user, got %d", respUser.StatusCode)
	}

	// 3. Admin uploading valid JPEG -> 201 Created + public photoUrl
	reqAdminJPEG := makeMultipartRequest(t, http.MethodPost, "/api/resources/photos/upload", nil, "file", "room.jpg", validJPEGSample)
	reqAdminJPEG.Header.Set("X-Test-User-Id", "admin-1")
	reqAdminJPEG.Header.Set("X-Test-Role", models.RoleAdmin)
	respJPEG, err := app.Test(reqAdminJPEG, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer respJPEG.Body.Close()
	if respJPEG.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(respJPEG.Body)
		t.Fatalf("expected 201 for admin JPEG upload, got %d: %s", respJPEG.StatusCode, string(body))
	}
	var jpegOut struct {
		PhotoURL string `json:"photoUrl"`
	}
	if err := json.NewDecoder(respJPEG.Body).Decode(&jpegOut); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(jpegOut.PhotoURL, storageServer.URL+"/storage/v1/object/public/resource-photos/resources/") ||
		!strings.HasSuffix(jpegOut.PhotoURL, ".jpg") {
		t.Fatalf("unexpected photoUrl: %s", jpegOut.PhotoURL)
	}

	// 4. Admin uploading valid PNG -> 201 Created + .png URL
	reqAdminPNG := makeMultipartRequest(t, http.MethodPost, "/api/resources/photos/upload", nil, "file", "vehicle.png", validPNGSample)
	reqAdminPNG.Header.Set("X-Test-User-Id", "admin-1")
	reqAdminPNG.Header.Set("X-Test-Role", models.RoleAdmin)
	respPNG, err := app.Test(reqAdminPNG, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer respPNG.Body.Close()
	if respPNG.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 for admin PNG upload, got %d", respPNG.StatusCode)
	}

	// 5. Unsupported/non-image content -> 400 INVALID_IMAGE
	reqBadType := makeMultipartRequest(t, http.MethodPost, "/api/resources/photos/upload", nil, "file", "fake.jpg", []byte("not an image payload"))
	reqBadType.Header.Set("X-Test-User-Id", "admin-1")
	reqBadType.Header.Set("X-Test-Role", models.RoleAdmin)
	respBadType, err := app.Test(reqBadType, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer respBadType.Body.Close()
	if respBadType.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-image payload, got %d", respBadType.StatusCode)
	}
	var badTypeErr apperror.AppError
	_ = json.NewDecoder(respBadType.Body).Decode(&badTypeErr)
	if badTypeErr.Code != "INVALID_IMAGE" {
		t.Fatalf("expected INVALID_IMAGE, got %s", badTypeErr.Code)
	}

	// 6. Oversized image (>10 MiB) -> 400 INVALID_IMAGE_SIZE
	oversized := make([]byte, maxStorageImageBytes+512)
	copy(oversized, validJPEGSample)
	reqOversized := makeMultipartRequest(t, http.MethodPost, "/api/resources/photos/upload", nil, "file", "huge.jpg", oversized)
	reqOversized.Header.Set("X-Test-User-Id", "admin-1")
	reqOversized.Header.Set("X-Test-Role", models.RoleAdmin)
	respOversized, err := app.Test(reqOversized, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer respOversized.Body.Close()
	if respOversized.StatusCode != http.StatusBadRequest && respOversized.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 400 or 413 for oversized image, got %d", respOversized.StatusCode)
	}
	if respOversized.StatusCode == http.StatusBadRequest {
		var sizeErr apperror.AppError
		_ = json.NewDecoder(respOversized.Body).Decode(&sizeErr)
		if sizeErr.Code != "INVALID_IMAGE_SIZE" {
			t.Fatalf("expected INVALID_IMAGE_SIZE, got %s", sizeErr.Code)
		}
	}

	if len(uploadedObjects) != 2 {
		t.Fatalf("expected exactly 2 valid uploads to reach Supabase Storage, got %d", len(uploadedObjects))
	}
}

func TestStorageHandler_ProofUpload_LifecycleCleanupAndUnavailable(t *testing.T) {
	var (
		mu             sync.Mutex
		uploadedPaths  []string
		deletedPaths   []string
		storageDown    bool
		signShouldFail bool
	)

	var serverURL string
	storageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		down := storageDown
		failSign := signShouldFail
		mu.Unlock()

		if down {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"upstream storage down"}`))
			return
		}

		switch {
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/storage/v1/object/booking-proofs/"):
			mu.Lock()
			uploadedPaths = append(uploadedPaths, strings.TrimPrefix(r.URL.Path, "/storage/v1/object/booking-proofs/"))
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"Key":"ok"}`))
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/storage/v1/object/sign/booking-proofs/"):
			if failSign {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			objPath := strings.TrimPrefix(r.URL.Path, "/storage/v1/object/sign/booking-proofs/")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"signedURL":"/object/sign/booking-proofs/` + objPath + `?token=signed-test"}`))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/storage/v1/object/booking-proofs/"):
			mu.Lock()
			deletedPaths = append(deletedPaths, strings.TrimPrefix(r.URL.Path, "/storage/v1/object/booking-proofs/"))
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer storageServer.Close()
	serverURL = storageServer.URL

	now := time.Now().UTC()
	bookings := &fakeBookingFinder{
		bookings: map[string]*models.Booking{
			"b-active": {
				ID:         "b-active",
				ResourceID: "res-1",
				UserID:     "owner-1",
				StartTime:  now.Add(-1 * time.Hour),
				EndTime:    now.Add(2 * time.Hour),
				Status:     models.BookingStatusInUse,
			},
			"b-revision": {
				ID:         "b-revision",
				ResourceID: "res-1",
				UserID:     "owner-1",
				StartTime:  now.Add(-72 * time.Hour),
				EndTime:    now.Add(-48 * time.Hour),
				Status:     models.BookingStatusNeedsRevision,
			},
			"b-pending": {
				ID:         "b-pending",
				ResourceID: "res-1",
				UserID:     "owner-1",
				StartTime:  now.Add(-1 * time.Hour),
				EndTime:    now.Add(2 * time.Hour),
				Status:     models.BookingStatusPending,
			},
		},
	}
	proofs := &fakeProofStore{}
	proofSvc := services.NewProofService(bookings)

	cfg := &config.Config{
		SupabaseURL:                    serverURL,
		SupabaseServiceKey:         "secret-service-role-do-not-leak",
		SupabaseStorageBucketResources: "resource-photos",
		SupabaseStorageBucketProofs:    "booking-proofs",
	}
	handler := NewStorageHandler(cfg, bookings, proofs, proofSvc)

	app := newHandlerTestApp()
	app.Post(
		"/api/bookings/:bookingId/proofs/upload",
		testAuthMiddleware,
		handler.UploadProof,
	)

	// 1. Booking owner uploads "before" proof on in_use booking -> 201 Created
	reqBefore := makeMultipartRequest(t, http.MethodPost, "/api/bookings/b-active/proofs/upload", map[string]string{"kind": "before"}, "file", "before.jpg", validJPEGSample)
	reqBefore.Header.Set("X-Test-User-Id", "owner-1")
	respBefore, err := app.Test(reqBefore, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer respBefore.Body.Close()
	if respBefore.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(respBefore.Body)
		t.Fatalf("expected 201 for owner before-proof upload, got %d: %s", respBefore.StatusCode, string(body))
	}
	var createdProof struct {
		ID        string `json:"id"`
		BookingID string `json:"bookingId"`
		Kind      string `json:"kind"`
		Path      string `json:"path"`
		URL       string `json:"url"`
	}
	if err := json.NewDecoder(respBefore.Body).Decode(&createdProof); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(createdProof.Path, "owner-1/b-active/before-") {
		t.Fatalf("unexpected proof path: %s", createdProof.Path)
	}
	if !strings.Contains(createdProof.URL, "/storage/v1/object/sign/booking-proofs/owner-1/b-active/before-") {
		t.Fatalf("unexpected signed URL: %s", createdProof.URL)
	}

	// 2. Booking owner uploads "after" proof on needs_revision booking (even after endDate) -> 201 Created
	reqRevision := makeMultipartRequest(t, http.MethodPost, "/api/bookings/b-revision/proofs/upload", map[string]string{"kind": "after"}, "file", "after.png", validPNGSample)
	reqRevision.Header.Set("X-Test-User-Id", "owner-1")
	respRevision, err := app.Test(reqRevision, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer respRevision.Body.Close()
	if respRevision.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(respRevision.Body)
		t.Fatalf("expected 201 for needs_revision after-proof upload, got %d: %s", respRevision.StatusCode, string(body))
	}

	// 3. Another user attempting to upload proof to owner-1's booking -> 403 FORBIDDEN
	reqOtherUser := makeMultipartRequest(t, http.MethodPost, "/api/bookings/b-active/proofs/upload", map[string]string{"kind": "before"}, "file", "before.jpg", validJPEGSample)
	reqOtherUser.Header.Set("X-Test-User-Id", "intruder-99")
	respOtherUser, err := app.Test(reqOtherUser, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer respOtherUser.Body.Close()
	if respOtherUser.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for unrelated user, got %d", respOtherUser.StatusCode)
	}

	// 4. Owner attempting to upload proof on pending booking -> 403 PROOF_NOT_ALLOWED
	reqInvalidStatus := makeMultipartRequest(t, http.MethodPost, "/api/bookings/b-pending/proofs/upload", map[string]string{"kind": "before"}, "file", "before.jpg", validJPEGSample)
	reqInvalidStatus.Header.Set("X-Test-User-Id", "owner-1")
	respInvalidStatus, err := app.Test(reqInvalidStatus, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer respInvalidStatus.Body.Close()
	if respInvalidStatus.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 PROOF_NOT_ALLOWED for pending booking, got %d", respInvalidStatus.StatusCode)
	}
	var statusErr apperror.AppError
	_ = json.NewDecoder(respInvalidStatus.Body).Decode(&statusErr)
	if statusErr.Code != "PROOF_NOT_ALLOWED" {
		t.Fatalf("expected PROOF_NOT_ALLOWED, got %s", statusErr.Code)
	}

	// 5. DB proof insert failure triggers Storage DELETE cleanup and returns 500 INTERNAL_ERROR
	proofs.mu.Lock()
	proofs.createErr = errors.New("simulated database failure")
	proofs.mu.Unlock()

	reqDBFail := makeMultipartRequest(t, http.MethodPost, "/api/bookings/b-active/proofs/upload", map[string]string{"kind": "after"}, "file", "after.jpg", validJPEGSample)
	reqDBFail.Header.Set("X-Test-User-Id", "owner-1")
	respDBFail, err := app.Test(reqDBFail, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer respDBFail.Body.Close()
	if respDBFail.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 on DB failure, got %d", respDBFail.StatusCode)
	}
	mu.Lock()
	if len(deletedPaths) != 1 || !strings.HasPrefix(deletedPaths[0], "owner-1/b-active/after-") {
		t.Fatalf("expected uploaded proof object to be deleted on DB failure, deleted=%v", deletedPaths)
	}
	mu.Unlock()

	proofs.mu.Lock()
	proofs.createErr = nil
	proofs.mu.Unlock()

	// 6. Supabase Storage unavailable -> returns 503 STORAGE_UNAVAILABLE without leaking service role key
	mu.Lock()
	storageDown = true
	mu.Unlock()

	reqStorageDown := makeMultipartRequest(t, http.MethodPost, "/api/bookings/b-active/proofs/upload", map[string]string{"kind": "after"}, "file", "after.jpg", validJPEGSample)
	reqStorageDown.Header.Set("X-Test-User-Id", "owner-1")
	respStorageDown, err := app.Test(reqStorageDown, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer respStorageDown.Body.Close()
	if respStorageDown.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when Supabase Storage is unavailable, got %d", respStorageDown.StatusCode)
	}
	rawErrBytes, _ := io.ReadAll(respStorageDown.Body)
	if strings.Contains(string(rawErrBytes), "secret-service-role-do-not-leak") {
		t.Fatal("response leaked service role key")
	}
	var storageErr apperror.AppError
	_ = json.Unmarshal(rawErrBytes, &storageErr)
	if storageErr.Code != "STORAGE_UNAVAILABLE" {
		t.Fatalf("expected STORAGE_UNAVAILABLE code, got %s", storageErr.Code)
	}
}

func TestStorageHandler_ListProofsSigned_And_LegacyProofSecurity(t *testing.T) {
	storageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/storage/v1/object/sign/booking-proofs/") {
			objPath := strings.TrimPrefix(r.URL.Path, "/storage/v1/object/sign/booking-proofs/")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"signedURL":"/object/sign/booking-proofs/` + objPath + `?token=ok"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer storageServer.Close()

	now := time.Now().UTC()
	bookings := &fakeBookingFinder{
		bookings: map[string]*models.Booking{
			"booking-1": {
				ID:         "booking-1",
				ResourceID: "res-1",
				UserID:     "owner-1",
				StartTime:  now.Add(-1 * time.Hour),
				EndTime:    now.Add(2 * time.Hour),
				Status:     models.BookingStatusInUse,
			},
		},
	}

	proofs := &fakeProofStore{
		proofs: map[string][]models.BookingProof{
			"booking-1": {
				{
					ID:         "p-standard",
					BookingID:  "booking-1",
					Kind:       models.ProofKindBefore,
					Path:       "owner-1/booking-1/before-1111.jpg",
					UploadedBy: "owner-1",
				},
				{
					ID:         "p-prefixed",
					BookingID:  "booking-1",
					Kind:       models.ProofKindAfter,
					Path:       "/booking-proofs/owner-1/booking-1/after-2222.png",
					UploadedBy: "owner-1",
				},
				{
					ID:         "p-external",
					BookingID:  "booking-1",
					Kind:       models.ProofKindAfter,
					Path:       "https://legacy.example.com/proofs/after-3333.jpg",
					UploadedBy: "owner-1",
				},
			},
		},
	}
	proofSvc := services.NewProofService(bookings)

	cfg := &config.Config{
		SupabaseURL:                    storageServer.URL,
		SupabaseServiceKey:         "test-service-role-key",
		SupabaseStorageBucketResources: "resource-photos",
		SupabaseStorageBucketProofs:    "booking-proofs",
	}
	storageHandler := NewStorageHandler(cfg, bookings, proofs, proofSvc)
	proofHandler := NewProofHandler(proofs, bookings, proofSvc)

	app := newHandlerTestApp()
	app.Get("/api/bookings/:bookingId/proofs/signed", testAuthMiddleware, storageHandler.ListProofsSigned)
	app.Post("/api/bookings/:bookingId/proofs", testAuthMiddleware, proofHandler.Create)

	// 1. Unrelated user requesting signed proof URLs -> 403 FORBIDDEN
	reqIntruder := httptest.NewRequest(http.MethodGet, "/api/bookings/booking-1/proofs/signed", nil)
	reqIntruder.Header.Set("X-Test-User-Id", "intruder-2")
	respIntruder, err := app.Test(reqIntruder, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer respIntruder.Body.Close()
	if respIntruder.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for unrelated user on /proofs/signed, got %d", respIntruder.StatusCode)
	}

	// 2. Booking owner requesting signed proof URLs -> 200 OK (handles standard, bucket-prefixed, and full URL paths)
	reqOwner := httptest.NewRequest(http.MethodGet, "/api/bookings/booking-1/proofs/signed", nil)
	reqOwner.Header.Set("X-Test-User-Id", "owner-1")
	respOwner, err := app.Test(reqOwner, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer respOwner.Body.Close()
	if respOwner.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(respOwner.Body)
		t.Fatalf("expected 200 for owner on /proofs/signed, got %d: %s", respOwner.StatusCode, string(body))
	}
	var signedItems []struct {
		ID   string `json:"id"`
		Path string `json:"path"`
		URL  string `json:"url"`
	}
	if err := json.NewDecoder(respOwner.Body).Decode(&signedItems); err != nil {
		t.Fatal(err)
	}
	if len(signedItems) != 3 {
		t.Fatalf("expected 3 signed proof items, got %d", len(signedItems))
	}
	if !strings.Contains(signedItems[0].URL, "/object/sign/booking-proofs/owner-1/booking-1/before-1111.jpg?token=ok") {
		t.Fatalf("unexpected signed URL for standard path: %s", signedItems[0].URL)
	}
	if !strings.Contains(signedItems[1].URL, "/object/sign/booking-proofs/owner-1/booking-1/after-2222.png?token=ok") {
		t.Fatalf("unexpected signed URL for bucket-prefixed path: %s", signedItems[1].URL)
	}
	if signedItems[2].URL != "https://legacy.example.com/proofs/after-3333.jpg" {
		t.Fatalf("unexpected URL for external legacy proof: %s", signedItems[2].URL)
	}

	// 3. Legacy POST /api/bookings/:bookingId/proofs rejects forged cross-booking path -> 400 INVALID_PROOF_PATH
	forgedBody := bytes.NewBufferString(`{"kind":"before","path":"other-user/other-booking/before-9999.jpg"}`)
	reqForged := httptest.NewRequest(http.MethodPost, "/api/bookings/booking-1/proofs", forgedBody)
	reqForged.Header.Set("Content-Type", "application/json")
	reqForged.Header.Set("X-Test-User-Id", "owner-1")
	respForged, err := app.Test(reqForged, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer respForged.Body.Close()
	if respForged.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for forged cross-booking proof path, got %d", respForged.StatusCode)
	}
	var forgedErr apperror.AppError
	_ = json.NewDecoder(respForged.Body).Decode(&forgedErr)
	if forgedErr.Code != "INVALID_PROOF_PATH" {
		t.Fatalf("expected INVALID_PROOF_PATH, got %s", forgedErr.Code)
	}

	// 4. Legacy POST /api/bookings/:bookingId/proofs accepts valid owner/booking path -> 201 Created
	validBody := bytes.NewBufferString(`{"kind":"before","path":"owner-1/booking-1/before-abcd1234.jpg"}`)
	reqValidLegacy := httptest.NewRequest(http.MethodPost, "/api/bookings/booking-1/proofs", validBody)
	reqValidLegacy.Header.Set("Content-Type", "application/json")
	reqValidLegacy.Header.Set("X-Test-User-Id", "owner-1")
	respValidLegacy, err := app.Test(reqValidLegacy, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer respValidLegacy.Body.Close()
	if respValidLegacy.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(respValidLegacy.Body)
		t.Fatalf("expected 201 for valid legacy proof path, got %d: %s", respValidLegacy.StatusCode, string(body))
	}

	// 5. Admin can also access /proofs/signed for any booking -> 200 OK
	reqAdmin := httptest.NewRequest(http.MethodGet, "/api/bookings/booking-1/proofs/signed", nil)
	reqAdmin.Header.Set("X-Test-User-Id", "admin-99")
	reqAdmin.Header.Set("X-Test-Role", models.RoleAdmin)
	respAdmin, err := app.Test(reqAdmin, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer respAdmin.Body.Close()
	if respAdmin.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for admin on /proofs/signed, got %d", respAdmin.StatusCode)
	}

	// 6. Stored proof row with cross-booking path is refused during signing -> 403 INVALID_PROOF_PATH
	proofs.mu.Lock()
	proofs.proofs["booking-1"] = append(proofs.proofs["booking-1"], models.BookingProof{
		ID:         "p-tampered",
		BookingID:  "booking-1",
		Kind:       models.ProofKindAfter,
		Path:       "other-owner/other-booking/after-tampered.jpg",
		UploadedBy: "owner-1",
	})
	proofs.mu.Unlock()

	reqTampered := httptest.NewRequest(http.MethodGet, "/api/bookings/booking-1/proofs/signed", nil)
	reqTampered.Header.Set("X-Test-User-Id", "owner-1")
	respTampered, err := app.Test(reqTampered, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer respTampered.Body.Close()
	if respTampered.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 INVALID_PROOF_PATH for tampered stored proof path, got %d", respTampered.StatusCode)
	}
}

func TestValidateResourceInput(t *testing.T) {
	if err := validateResourceInput(&models.ResourceInput{
		Name: "Meeting Room A",
		Type: models.ResourceTypeRoom,
		Color: "#0EA5E9",
	}); err != nil {
		t.Fatalf("expected valid resource input to pass, got %v", err)
	}

	if err := validateResourceInput(&models.ResourceInput{
		Name: "",
		Type: models.ResourceTypeRoom,
	}); err == nil {
		t.Fatal("expected empty resource name to fail validation")
	}

	if err := validateResourceInput(&models.ResourceInput{
		Name: "Invalid Type",
		Type: "helicopter",
	}); err == nil {
		t.Fatal("expected unsupported resource type to fail validation")
	}

	if err := validateResourceInput(&models.ResourceInput{
		Name:  "Bad Color",
		Type:  models.ResourceTypeCar,
		Color: "not-a-hex-color",
	}); err == nil {
		t.Fatal("expected invalid hex color to fail validation")
	}
}
