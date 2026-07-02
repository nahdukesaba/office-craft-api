package db

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"

	"office-craft-api/internal/config"
)

// Connect opens a pgx connection pool to the configured Postgres database.
func Connect(cfg *config.Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing database url: %w", err)
	}
	poolCfg.MaxConns = 10

	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		return nil, fmt.Errorf("creating connection pool: %w", err)
	}

	if err := pool.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return pool, nil
}

// RunMigrations applies all pending "up" migrations found in cfg.MigrationsPath.
// It is safe to call on every startup; golang-migrate is a no-op if already current.
func RunMigrations(cfg *config.Config) error {
	sourceURL := "file://" + cfg.MigrationsPath

	m, err := migrate.New(sourceURL, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("initializing migrator: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			log.Println("migrations: database schema already up to date")
			return nil
		}
		return fmt.Errorf("running migrations: %w", err)
	}

	log.Println("migrations: applied successfully")
	return nil
}
