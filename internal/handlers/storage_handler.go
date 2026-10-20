package handlers

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/config"
	"office-craft-api/internal/middleware"
	"office-craft-api/internal/models"
	"office-craft-api/internal/services"
)

const maxStorageImageBytes = 10 * 1024 * 1024

type storageBookingFinder interface {
	GetByID(ctx context.Context, id string) (*models.Booking, error)
}

type storageProofStore interface {
	Create(ctx context.Context, bookingID, kind, path, uploadedBy string) (*models.BookingProof, error)
	ListByBooking(ctx context.Context, bookingID string) ([]models.BookingProof, error)
}

type storageProofValidator interface {
	ValidateUpload(ctx context.Context, bookingID, kind string) (*models.Booking, error)
}

type StorageHandler struct {
	storage         *services.StorageService
	bookings        storageBookingFinder
	proofs          storageProofStore
	proofRules      storageProofValidator
	resourcesBucket string
	proofsBucket    string
}

func NewStorageHandler(
	cfg *config.Config,
	bookings storageBookingFinder,
	proofs storageProofStore,
	rules storageProofValidator,
) *StorageHandler {
	resourcesBucket := strings.TrimSpace(cfg.SupabaseStorageBucketResources)
	if resourcesBucket == "" {
		resourcesBucket = "resource-photos"
	}

	proofsBucket := strings.TrimSpace(cfg.SupabaseStorageBucketProofs)
	if proofsBucket == "" {
		proofsBucket = "booking-proofs"
	}

	return &StorageHandler{
		storage:         services.NewStorageService(cfg),
		bookings:        bookings,
		proofs:          proofs,
		proofRules:      rules,
		resourcesBucket: resourcesBucket,
		proofsBucket:    proofsBucket,
	}
}

func storageImageType(content []byte) (string, string, error) {
	switch http.DetectContentType(content) {
	case "image/jpeg":
		return "image/jpeg", ".jpg", nil
	case "image/png":
		return "image/png", ".png", nil
	case "image/webp":
		return "image/webp", ".webp", nil
	case "image/gif":
		return "image/gif", ".gif", nil
	default:
		return "", "", apperror.BadRequest(
			"INVALID_IMAGE",
			"upload a JPEG, PNG, WebP, or GIF image",
		)
	}
}

func readStorageImage(
	header *multipart.FileHeader,
) ([]byte, string, string, error) {
	if header == nil ||
		header.Size <= 0 ||
		header.Size > maxStorageImageBytes {
		return nil, "", "", apperror.BadRequest(
			"INVALID_IMAGE_SIZE",
			"image must be between 1 byte and 10 MiB",
		)
	}

	f, err := header.Open()
	if err != nil {
		return nil, "", "", apperror.BadRequest(
			"INVALID_IMAGE",
			"cannot read uploaded image",
		)
	}
	defer f.Close()

	data, err := io.ReadAll(
		io.LimitReader(f, maxStorageImageBytes+1),
	)
	if err != nil {
		return nil, "", "", apperror.BadRequest(
			"INVALID_IMAGE",
			"invalid image content",
		)
	}
	if len(data) == 0 || len(data) > maxStorageImageBytes {
		return nil, "", "", apperror.BadRequest(
			"INVALID_IMAGE_SIZE",
			"image must be between 1 byte and 10 MiB",
		)
	}

	mime, ext, err := storageImageType(data)
	return data, mime, ext, err
}

func storageRandomKey() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", random), nil
}

func (h *StorageHandler) UploadResourcePhoto(
	c *fiber.Ctx,
) error {
	file, err := c.FormFile("file")
	if err != nil {
		return apperror.BadRequest(
			"IMAGE_REQUIRED",
			"multipart file field is required",
		)
	}

	data, mime, ext, err := readStorageImage(file)
	if err != nil {
		return err
	}

	key, err := storageRandomKey()
	if err != nil {
		return apperror.Internal(
			"failed to prepare image path",
		)
	}

	path := "resources/" + key + ext

	if err := h.storage.Upload(
		c.UserContext(),
		h.resourcesBucket,
		path,
		mime,
		data,
	); err != nil {
		log.Printf("resource photo upload error: %v", err)
		return apperror.New(
			fiber.StatusServiceUnavailable,
			"STORAGE_UNAVAILABLE",
			"resource storage unavailable",
		)
	}

	publicURL, err := h.storage.PublicURL(
		h.resourcesBucket,
		path,
	)
	if err != nil {
		return apperror.Internal(
			"cannot create public image URL",
		)
	}

	return c.Status(fiber.StatusCreated).JSON(
		fiber.Map{"photoUrl": publicURL},
	)
}

func (h *StorageHandler) bookingForUser(
	c *fiber.Ctx,
) (*models.Booking, error) {
	b, err := h.bookings.GetByID(
		c.UserContext(),
		c.Params("bookingId"),
	)
	if err != nil {
		return nil, apperror.Internal(
			"failed to load booking",
		)
	}
	if b == nil {
		return nil, apperror.NotFound(
			"BOOKING_NOT_FOUND",
			"booking not found",
		)
	}

	if middleware.RoleFromCtx(c) != models.RoleAdmin &&
		b.UserID != middleware.UserIDFromCtx(c) {
		return nil, apperror.Forbidden(
			"FORBIDDEN",
			"you do not have access to this booking",
		)
	}
	return b, nil
}

func (h *StorageHandler) deleteUploadedProof(
	path string,
) {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		8*time.Second,
	)
	defer cancel()

	if err := h.storage.Delete(
		ctx,
		h.proofsBucket,
		path,
	); err != nil {
		log.Printf("proof storage cleanup failed: %v", err)
	}
}

func (h *StorageHandler) UploadProof(
	c *fiber.Ctx,
) error {
	b, err := h.bookingForUser(c)
	if err != nil {
		return err
	}

	kind := strings.TrimSpace(c.FormValue("kind"))
	if kind != models.ProofKindBefore &&
		kind != models.ProofKindAfter {
		return apperror.BadRequest(
			"INVALID_KIND",
			"kind must be 'before' or 'after'",
		)
	}

	if _, err := h.proofRules.ValidateUpload(
		c.UserContext(),
		b.ID,
		kind,
	); err != nil {
		return err
	}

	file, err := c.FormFile("file")
	if err != nil {
		return apperror.BadRequest(
			"IMAGE_REQUIRED",
			"multipart file field is required",
		)
	}

	data, mime, ext, err := readStorageImage(file)
	if err != nil {
		return err
	}

	key, err := storageRandomKey()
	if err != nil {
		return apperror.Internal(
			"failed to prepare proof path",
		)
	}

	path := fmt.Sprintf(
		"%s/%s/%s-%s%s",
		strings.TrimSpace(b.UserID),
		strings.TrimSpace(b.ID),
		kind,
		key,
		ext,
	)

	if err := h.storage.Upload(
		c.UserContext(),
		h.proofsBucket,
		path,
		mime,
		data,
	); err != nil {
		log.Printf("proof upload error: %v", err)
		return apperror.New(
			fiber.StatusServiceUnavailable,
			"STORAGE_UNAVAILABLE",
			"proof storage unavailable",
		)
	}

	signedURL, err := h.storage.SignedURL(
		c.UserContext(),
		h.proofsBucket,
		path,
	)
	if err != nil {
		h.deleteUploadedProof(path)
		return apperror.New(
			fiber.StatusServiceUnavailable,
			"STORAGE_UNAVAILABLE",
			"cannot prepare proof URL",
		)
	}

	saved, err := h.proofs.Create(
		c.UserContext(),
		b.ID,
		kind,
		path,
		middleware.UserIDFromCtx(c),
	)
	if err != nil {
		h.deleteUploadedProof(path)
		return apperror.Internal(
			"failed to record proof",
		)
	}

	return c.Status(fiber.StatusCreated).JSON(
		struct {
			*models.BookingProof
			URL string `json:"url"`
		}{
			BookingProof: saved,
			URL:          signedURL,
		},
	)
}

func normalizeStoredProofPath(bucket, rawPath string) string {
	cleaned := strings.TrimSpace(rawPath)
	cleaned = strings.TrimLeft(cleaned, "/")
	if bucket != "" && strings.HasPrefix(cleaned, bucket+"/") {
		cleaned = strings.TrimPrefix(cleaned, bucket+"/")
	}
	return cleaned
}

func isValidProofObjectForBooking(b *models.Booking, uploadedBy, objectPath string) bool {
	parts := strings.Split(objectPath, "/")
	if len(parts) != 3 {
		return false
	}

	ownerFolder := strings.TrimSpace(parts[0])
	bookingFolder := strings.TrimSpace(parts[1])
	fileName := strings.TrimSpace(parts[2])

	if ownerFolder == "" || bookingFolder == "" || fileName == "" {
		return false
	}

	if bookingFolder != strings.TrimSpace(b.ID) {
		return false
	}

	if ownerFolder != strings.TrimSpace(b.UserID) &&
		(strings.TrimSpace(uploadedBy) == "" || ownerFolder != strings.TrimSpace(uploadedBy)) {
		return false
	}

	return true
}

func (h *StorageHandler) ListProofsSigned(
	c *fiber.Ctx,
) error {
	b, err := h.bookingForUser(c)
	if err != nil {
		return err
	}

	rows, err := h.proofs.ListByBooking(
		c.UserContext(),
		b.ID,
	)
	if err != nil {
		return apperror.Internal(
			"failed to list proofs",
		)
	}

	type item struct {
		models.BookingProof
		URL string `json:"url"`
	}

	result := make([]item, 0, len(rows))

	for _, row := range rows {
		rawPath := strings.TrimSpace(row.Path)
		if strings.HasPrefix(rawPath, "http://") || strings.HasPrefix(rawPath, "https://") {
			result = append(result, item{BookingProof: row, URL: rawPath})
			continue
		}

		objectPath := normalizeStoredProofPath(h.proofsBucket, rawPath)
		if !isValidProofObjectForBooking(b, row.UploadedBy, objectPath) {
			log.Printf("refusing to sign proof %s with mismatched booking path %q", row.ID, rawPath)
			return apperror.Forbidden(
				"INVALID_PROOF_PATH",
				"stored proof path does not match booking context",
			)
		}

		signed, err := h.storage.SignedURL(
			c.UserContext(),
			h.proofsBucket,
			objectPath,
		)
		if err != nil {
			log.Printf("proof URL signing error: %v", err)
			return apperror.New(
				fiber.StatusServiceUnavailable,
				"STORAGE_UNAVAILABLE",
				"proof storage unavailable",
			)
		}
		result = append(
			result,
			item{BookingProof: row, URL: signed},
		)
	}

	return c.JSON(result)
}
