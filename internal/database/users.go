package database

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrUserNotFound = errors.New("user not found")
	ErrUserExists   = errors.New("user already exists")
)

type User struct {
	ID        int64
	Name      string
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

func GenerateToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return "tcs_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func (db *DB) CreateUser(ctx context.Context, name string) (User, string, error) {
	name = strings.TrimSpace(name)
	if err := validateUserName(name); err != nil {
		return User{}, "", err
	}
	token, err := GenerateToken()
	if err != nil {
		return User{}, "", err
	}
	hash := sha256.Sum256([]byte(token))
	now := time.Now().UTC().UnixMilli()

	result, err := db.sql.ExecContext(ctx, `
		INSERT INTO users(name, token_hash, enabled, created_at_ms, updated_at_ms)
		VALUES (?, ?, 1, ?, ?)
	`, name, hash[:], now, now)
	if err != nil {
		if isUniqueConstraint(err) {
			return User{}, "", ErrUserExists
		}
		return User{}, "", fmt.Errorf("create user: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return User{}, "", fmt.Errorf("read created user ID: %w", err)
	}
	return User{
		ID:        id,
		Name:      name,
		Enabled:   true,
		CreatedAt: fromMillis(now),
		UpdatedAt: fromMillis(now),
	}, token, nil
}

func (db *DB) RotateUserToken(ctx context.Context, name string) (string, error) {
	token, err := GenerateToken()
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(token))
	now := time.Now().UTC().UnixMilli()
	result, err := db.sql.ExecContext(ctx, `
		UPDATE users SET token_hash = ?, updated_at_ms = ? WHERE name = ? COLLATE NOCASE
	`, hash[:], now, strings.TrimSpace(name))
	if err != nil {
		return "", fmt.Errorf("rotate user token: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("read rotated user count: %w", err)
	}
	if affected == 0 {
		return "", ErrUserNotFound
	}
	return token, nil
}

func (db *DB) SetUserEnabled(ctx context.Context, name string, enabled bool) error {
	value := 0
	if enabled {
		value = 1
	}
	result, err := db.sql.ExecContext(ctx, `
		UPDATE users SET enabled = ?, updated_at_ms = ? WHERE name = ? COLLATE NOCASE
	`, value, time.Now().UTC().UnixMilli(), strings.TrimSpace(name))
	if err != nil {
		return fmt.Errorf("update user state: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated user count: %w", err)
	}
	if affected == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (db *DB) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT id, name, enabled, created_at_ms, updated_at_ms
		FROM users ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var user User
		var enabled int
		var createdAt, updatedAt int64
		if err := rows.Scan(&user.ID, &user.Name, &enabled, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		user.Enabled = enabled == 1
		user.CreatedAt = fromMillis(createdAt)
		user.UpdatedAt = fromMillis(updatedAt)
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return users, nil
}

func (db *DB) Authenticate(ctx context.Context, token string) (User, error) {
	if token == "" || len(token) > 512 {
		return User{}, ErrUserNotFound
	}
	hash := sha256.Sum256([]byte(token))
	var user User
	var enabled int
	var createdAt, updatedAt int64
	err := db.sql.QueryRowContext(ctx, `
		SELECT id, name, enabled, created_at_ms, updated_at_ms
		FROM users WHERE token_hash = ? AND enabled = 1
	`, hash[:]).Scan(&user.ID, &user.Name, &enabled, &createdAt, &updatedAt)
	if isNotFound(err) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("authenticate user: %w", err)
	}
	user.Enabled = enabled == 1
	user.CreatedAt = fromMillis(createdAt)
	user.UpdatedAt = fromMillis(updatedAt)
	return user, nil
}

func validateUserName(name string) error {
	if name == "" {
		return errors.New("user name must not be empty")
	}
	if !utf8.ValidString(name) {
		return errors.New("user name must be valid UTF-8")
	}
	if utf8.RuneCountInString(name) > 128 {
		return errors.New("user name must not exceed 128 characters")
	}
	return nil
}

func isUniqueConstraint(err error) bool {
	var sqliteErr interface{ Code() int }
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code() == 2067 || sqliteErr.Code() == 1555
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

func fromMillis(value int64) time.Time {
	return time.UnixMilli(value).UTC()
}
