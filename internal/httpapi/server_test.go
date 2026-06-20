package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"tabby-config-sync/internal/database"
)

type testEnvironment struct {
	db         *database.DB
	server     *httptest.Server
	aliceToken string
	bobToken   string
}

func newTestEnvironment(t *testing.T, maxBodyBytes int64) testEnvironment {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, aliceToken, err := db.CreateUser(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	_, bobToken, err := db.CreateUser(context.Background(), "bob")
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(New(db, logger, maxBodyBytes, "test"))
	t.Cleanup(func() {
		server.Close()
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return testEnvironment{
		db:         db,
		server:     server,
		aliceToken: aliceToken,
		bobToken:   bobToken,
	}
}

func (environment testEnvironment) request(
	t *testing.T,
	method, path, token string,
	body any,
) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, environment.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeResponse[T any](t *testing.T, response *http.Response) T {
	t.Helper()
	defer response.Body.Close()
	var value T
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestTabbySyncProtocolFlow(t *testing.T) {
	environment := newTestEnvironment(t, 1<<20)

	response := environment.request(t, http.MethodGet, "/api/1/user", "", nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated user status=%d", response.StatusCode)
	}
	response.Body.Close()

	response = environment.request(t, http.MethodGet, "/api/1/user", environment.aliceToken, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authenticated user status=%d", response.StatusCode)
	}
	user := decodeResponse[map[string]any](t, response)
	if user["username"] != "alice" {
		t.Fatalf("unexpected user response: %#v", user)
	}
	if _, exposed := user["config_sync_token"]; exposed {
		t.Fatal("user response exposed the sync token")
	}

	response = environment.request(t, http.MethodPost, "/api/1/configs", environment.aliceToken, map[string]any{
		"name": "Workstation",
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d", response.StatusCode)
	}
	created := decodeResponse[configResponse](t, response)
	if created.ID <= 0 || created.Content != "{}" || created.Name != "Workstation" {
		t.Fatalf("unexpected created config: %#v", created)
	}
	if _, err := time.Parse(time.RFC3339Nano, created.ModifiedAt); err != nil {
		t.Fatalf("invalid modified_at: %v", err)
	}

	response = environment.request(t, http.MethodGet, "/api/1/configs", environment.aliceToken, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list status=%d", response.StatusCode)
	}
	configs := decodeResponse[[]configResponse](t, response)
	if len(configs) != 1 || configs[0].ID != created.ID {
		t.Fatalf("unexpected config list: %#v", configs)
	}

	yamlContent := "version: 7\nprofiles:\n  - name: Shell\n"
	response = environment.request(
		t,
		http.MethodPatch,
		"/api/1/configs/"+intString(created.ID),
		environment.aliceToken,
		map[string]any{
			"content":                yamlContent,
			"last_used_with_version": "1.0.223",
		},
	)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("patch status=%d", response.StatusCode)
	}
	updated := decodeResponse[configResponse](t, response)
	if updated.Content != yamlContent {
		t.Fatalf("content changed during storage: %q", updated.Content)
	}
	if updated.LastUsedWithVersion == nil || *updated.LastUsedWithVersion != "1.0.223" {
		t.Fatalf("version not stored: %#v", updated.LastUsedWithVersion)
	}
	createdTime, _ := time.Parse(time.RFC3339Nano, created.ModifiedAt)
	updatedTime, _ := time.Parse(time.RFC3339Nano, updated.ModifiedAt)
	if !updatedTime.After(createdTime) {
		t.Fatalf("modified_at did not advance: created=%s updated=%s", created.ModifiedAt, updated.ModifiedAt)
	}

	response = environment.request(
		t,
		http.MethodGet,
		"/api/1/configs/"+intString(created.ID),
		environment.bobToken,
		nil,
	)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-user read status=%d", response.StatusCode)
	}
	response.Body.Close()

	response = environment.request(
		t,
		http.MethodDelete,
		"/api/1/configs/"+intString(created.ID),
		environment.aliceToken,
		nil,
	)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status=%d", response.StatusCode)
	}
	response.Body.Close()
}

func TestStrictInputAndBodyLimit(t *testing.T) {
	environment := newTestEnvironment(t, 128)

	response := environment.request(t, http.MethodPost, "/api/1/configs", environment.aliceToken, map[string]any{
		"name":    "test",
		"unknown": true,
	})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d", response.StatusCode)
	}
	response.Body.Close()

	response = environment.request(t, http.MethodPost, "/api/1/configs", environment.aliceToken, map[string]any{
		"name":    "large",
		"content": strings.Repeat("x", 256),
	})
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("large body status=%d", response.StatusCode)
	}
	response.Body.Close()
}

func intString(value int64) string {
	return strconv.FormatInt(value, 10)
}
