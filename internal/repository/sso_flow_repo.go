package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"office-craft-api/internal/models"
)

type BrowserSSOFlowRepository struct {
	pool *pgxpool.Pool
}

func NewBrowserSSOFlowRepository(pool *pgxpool.Pool) *BrowserSSOFlowRepository {
	return &BrowserSSOFlowRepository{pool: pool}
}

func (r *BrowserSSOFlowRepository) Create(
	ctx context.Context,
	flow *models.BrowserSSOFlow,
) error {
	_, err := r.pool.Exec(ctx, `
INSERT INTO public.silapet_sso_flows (state_hash, expires_at)
VALUES ($1, $2)
`, flow.StateHash, flow.ExpiresAt)
	if err != nil {
		return fmt.Errorf("create SILAPET browser SSO flow: %w", err)
	}
	return nil
}

func (r *BrowserSSOFlowRepository) Consume(
	ctx context.Context,
	stateHash []byte,
) (*models.BrowserSSOFlow, error) {
	row := r.pool.QueryRow(ctx, `
UPDATE public.silapet_sso_flows
SET used_at = now()
WHERE state_hash = $1 AND used_at IS NULL AND expires_at > now()
RETURNING state_hash, created_at, expires_at, used_at
`, stateHash)
	var flow models.BrowserSSOFlow
	if err := row.Scan(&flow.StateHash, &flow.CreatedAt, &flow.ExpiresAt, &flow.UsedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("consume SILAPET browser SSO flow: %w", err)
	}
	return &flow, nil
}
