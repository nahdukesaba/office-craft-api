package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GraphTokenRepository keeps the newest Microsoft Graph refresh token.
type GraphTokenRepository struct {
	pool *pgxpool.Pool
}

func NewGraphTokenRepository(pool *pgxpool.Pool) *GraphTokenRepository {
	return &GraphTokenRepository{pool: pool}
}

// GetRefreshToken returns "" when no token has been stored yet.
func (r *GraphTokenRepository) GetRefreshToken(ctx context.Context, id string) (string, error) {
	var token string
	err := r.pool.QueryRow(ctx, `SELECT refresh_token FROM public.graph_tokens WHERE id = $1`, id).Scan(&token)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("load graph refresh token: %w", err)
	}
	return token, nil
}

func (r *GraphTokenRepository) SaveRefreshToken(ctx context.Context, id, token string) error {
	_, err := r.pool.Exec(ctx, `
INSERT INTO public.graph_tokens (id, refresh_token, updated_at)
VALUES ($1, $2, now())
ON CONFLICT (id) DO UPDATE SET refresh_token = EXCLUDED.refresh_token, updated_at = now()
`, id, token)
	if err != nil {
		return fmt.Errorf("save graph refresh token: %w", err)
	}
	return nil
}
