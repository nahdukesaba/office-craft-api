package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"office-craft-api/internal/models"
)

type BookingRulesRepository struct {
	pool *pgxpool.Pool
}

func NewBookingRulesRepository(pool *pgxpool.Pool) *BookingRulesRepository {
	return &BookingRulesRepository{pool: pool}
}

func (r *BookingRulesRepository) Get(ctx context.Context) (models.BookingRules, error) {
	var rules models.BookingRules
	err := r.pool.QueryRow(ctx, `
		SELECT block_resource_open_usage, block_user_unfinished FROM public.booking_rules WHERE id
	`).Scan(&rules.BlockResourceOpenUsage, &rules.BlockUserUnfinished)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.BookingRules{}, nil
	}
	return rules, err
}

func (r *BookingRulesRepository) Save(ctx context.Context, rules models.BookingRules, actorID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO public.booking_rules (id, block_resource_open_usage, block_user_unfinished, updated_by, updated_at)
		VALUES (true, $1, $2, $3::uuid, now())
		ON CONFLICT (id) DO UPDATE SET
			block_resource_open_usage = EXCLUDED.block_resource_open_usage,
			block_user_unfinished = EXCLUDED.block_user_unfinished,
			updated_by = EXCLUDED.updated_by,
			updated_at = now()
	`, rules.BlockResourceOpenUsage, rules.BlockUserUnfinished, actorID)
	return err
}
