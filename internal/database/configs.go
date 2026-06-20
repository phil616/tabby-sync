package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrConfigNotFound = errors.New("config not found")

type Config struct {
	ID                  int64
	Name                string
	Content             string
	LastUsedWithVersion *string
	CreatedAt           time.Time
	ModifiedAt          time.Time
}

type NewConfig struct {
	Name                string
	Content             string
	LastUsedWithVersion *string
}

type ConfigPatch struct {
	Name                *string
	Content             *string
	VersionSet          bool
	LastUsedWithVersion *string
}

func (db *DB) ListConfigs(ctx context.Context, userID int64) ([]Config, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT id, name, content, last_used_with_version, created_at_ms, modified_at_ms
		FROM configs WHERE user_id = ? ORDER BY id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list configs: %w", err)
	}
	defer rows.Close()

	configs := make([]Config, 0)
	for rows.Next() {
		config, err := scanConfig(rows)
		if err != nil {
			return nil, err
		}
		configs = append(configs, config)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate configs: %w", err)
	}
	return configs, nil
}

func (db *DB) GetConfig(ctx context.Context, userID, configID int64) (Config, error) {
	config, err := scanConfig(db.sql.QueryRowContext(ctx, `
		SELECT id, name, content, last_used_with_version, created_at_ms, modified_at_ms
		FROM configs WHERE id = ? AND user_id = ?
	`, configID, userID))
	if isNotFound(err) {
		return Config{}, ErrConfigNotFound
	}
	return config, err
}

func (db *DB) CreateConfig(ctx context.Context, userID int64, input NewConfig) (Config, error) {
	now := time.Now().UTC().UnixMilli()
	result, err := db.sql.ExecContext(ctx, `
		INSERT INTO configs(
			user_id, name, content, last_used_with_version, created_at_ms, modified_at_ms
		) VALUES (?, ?, ?, ?, ?, ?)
	`, userID, input.Name, input.Content, input.LastUsedWithVersion, now, now)
	if err != nil {
		return Config{}, fmt.Errorf("create config: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Config{}, fmt.Errorf("read created config ID: %w", err)
	}
	return db.GetConfig(ctx, userID, id)
}

func (db *DB) UpdateConfig(ctx context.Context, userID, configID int64, patch ConfigPatch) (Config, error) {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return Config{}, fmt.Errorf("begin config update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	config, err := scanConfig(tx.QueryRowContext(ctx, `
		SELECT id, name, content, last_used_with_version, created_at_ms, modified_at_ms
		FROM configs WHERE id = ? AND user_id = ?
	`, configID, userID))
	if isNotFound(err) {
		return Config{}, ErrConfigNotFound
	}
	if err != nil {
		return Config{}, err
	}

	if patch.Name != nil {
		config.Name = *patch.Name
	}
	if patch.Content != nil {
		config.Content = *patch.Content
	}
	if patch.VersionSet {
		config.LastUsedWithVersion = patch.LastUsedWithVersion
	}
	now := time.Now().UTC().UnixMilli()
	previous := config.ModifiedAt.UnixMilli()
	if now <= previous {
		now = previous + 1
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE configs
		SET name = ?, content = ?, last_used_with_version = ?, modified_at_ms = ?
		WHERE id = ? AND user_id = ?
	`, config.Name, config.Content, config.LastUsedWithVersion, now, configID, userID)
	if err != nil {
		return Config{}, fmt.Errorf("update config: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Config{}, fmt.Errorf("read updated config count: %w", err)
	}
	if affected != 1 {
		return Config{}, ErrConfigNotFound
	}
	if err := tx.Commit(); err != nil {
		return Config{}, fmt.Errorf("commit config update: %w", err)
	}
	config.ModifiedAt = fromMillis(now)
	return config, nil
}

func (db *DB) DeleteConfig(ctx context.Context, userID, configID int64) error {
	result, err := db.sql.ExecContext(ctx, `
		DELETE FROM configs WHERE id = ? AND user_id = ?
	`, configID, userID)
	if err != nil {
		return fmt.Errorf("delete config: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted config count: %w", err)
	}
	if affected == 0 {
		return ErrConfigNotFound
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanConfig(row scanner) (Config, error) {
	var config Config
	var version sql.NullString
	var createdAt, modifiedAt int64
	if err := row.Scan(
		&config.ID,
		&config.Name,
		&config.Content,
		&version,
		&createdAt,
		&modifiedAt,
	); err != nil {
		if isNotFound(err) {
			return Config{}, err
		}
		return Config{}, fmt.Errorf("scan config: %w", err)
	}
	if version.Valid {
		config.LastUsedWithVersion = &version.String
	}
	config.CreatedAt = fromMillis(createdAt)
	config.ModifiedAt = fromMillis(modifiedAt)
	return config, nil
}
