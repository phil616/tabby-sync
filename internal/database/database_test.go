package database

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return db
}

func TestUserLifecycleAndAuthentication(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	user, token, err := db.CreateUser(ctx, "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || user.Name != "Alice" || !user.Enabled {
		t.Fatalf("unexpected created user: %#v token=%q", user, token)
	}
	if _, _, err := db.CreateUser(ctx, "alice"); !errors.Is(err, ErrUserExists) {
		t.Fatalf("expected duplicate user error, got %v", err)
	}

	authenticated, err := db.Authenticate(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if authenticated.ID != user.ID {
		t.Fatalf("authenticated user ID %d, want %d", authenticated.ID, user.ID)
	}

	rotated, err := db.RotateUserToken(ctx, "ALICE")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Authenticate(ctx, token); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("old token remained valid: %v", err)
	}
	if _, err := db.Authenticate(ctx, rotated); err != nil {
		t.Fatalf("new token rejected: %v", err)
	}

	if err := db.SetUserEnabled(ctx, "alice", false); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Authenticate(ctx, rotated); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("disabled user authenticated: %v", err)
	}
}

func TestConfigIsolationAndMonotonicModificationTime(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	alice, _, err := db.CreateUser(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, _, err := db.CreateUser(ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}

	config, err := db.CreateConfig(ctx, alice.ID, NewConfig{
		Name:    "Laptop",
		Content: "version: 7\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetConfig(ctx, bob.ID, config.ID); !errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("cross-user read was not blocked: %v", err)
	}

	previous := config.ModifiedAt
	for i := 0; i < 10; i++ {
		content := "version: 7\nvalue: changed\n"
		config, err = db.UpdateConfig(ctx, alice.ID, config.ID, ConfigPatch{Content: &content})
		if err != nil {
			t.Fatal(err)
		}
		if !config.ModifiedAt.After(previous) {
			t.Fatalf("modified_at did not increase: previous=%s current=%s", previous, config.ModifiedAt)
		}
		previous = config.ModifiedAt
	}
	if config.ModifiedAt.Sub(config.CreatedAt) < 10*time.Millisecond {
		t.Fatalf("expected millisecond-separated updates, got %s", config.ModifiedAt.Sub(config.CreatedAt))
	}
}

func TestConcurrentConfigUpdatesRemainUsable(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	user, _, err := db.CreateUser(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	config, err := db.CreateConfig(ctx, user.ID, NewConfig{Name: "Concurrent", Content: "{}"})
	if err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	errorsChannel := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			content := "updated: true\n"
			_, err := db.UpdateConfig(ctx, user.ID, config.ID, ConfigPatch{Content: &content})
			errorsChannel <- err
		}()
	}
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Errorf("concurrent update: %v", err)
		}
	}
	if _, err := db.GetConfig(ctx, user.ID, config.ID); err != nil {
		t.Fatal(err)
	}
}
