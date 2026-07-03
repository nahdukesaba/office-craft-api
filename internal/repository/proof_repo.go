package repository

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"office-craft-api/internal/models"
)

type ProofRepository struct {
	pool *pgxpool.Pool
}

func NewProofRepository(pool *pgxpool.Pool) *ProofRepository {
	return &ProofRepository{pool: pool}
}

func (r *ProofRepository) ListByBooking(ctx context.Context, bookingID string) ([]models.BookingProof, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, booking_id, kind, path, uploaded_by, created_at
		FROM public.booking_proofs WHERE booking_id = $1 AND deleted_at IS NULL ORDER BY created_at ASC
	`, bookingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.BookingProof
	for rows.Next() {
		var p models.BookingProof
		if err := rows.Scan(&p.ID, &p.BookingID, &p.Kind, &p.Path, &p.UploadedBy, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *ProofRepository) Create(ctx context.Context, bookingID, kind, path, uploadedBy string) (*models.BookingProof, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO public.booking_proofs (booking_id, kind, path, uploaded_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id, booking_id, kind, path, uploaded_by, created_at
	`, bookingID, kind, path, uploadedBy)

	var p models.BookingProof
	if err := row.Scan(&p.ID, &p.BookingID, &p.Kind, &p.Path, &p.UploadedBy, &p.CreatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

// SoftDelete marks a proof as deleted without removing the row. No route
// currently exposes deleting a proof, but the repository is ready if one
// is added later.
func (r *ProofRepository) SoftDelete(ctx context.Context, id string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE public.booking_proofs SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

var _ = pgx.ErrNoRows
