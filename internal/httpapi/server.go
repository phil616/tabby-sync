package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"tabby-config-sync/internal/database"
)

type API struct {
	db           *database.DB
	logger       *slog.Logger
	maxBodyBytes int64
	version      string
}

type userContextKey struct{}

func New(db *database.DB, logger *slog.Logger, maxBodyBytes int64, version string) http.Handler {
	api := &API{
		db:           db,
		logger:       logger,
		maxBodyBytes: maxBodyBytes,
		version:      version,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", api.root)
	mux.HandleFunc("GET /healthz", api.health)
	mux.HandleFunc("GET /readyz", api.ready)
	mux.Handle("GET /api/1/user", api.authenticate(http.HandlerFunc(api.getUser)))
	mux.Handle("GET /api/1/configs", api.authenticate(http.HandlerFunc(api.listConfigs)))
	mux.Handle("POST /api/1/configs", api.authenticate(http.HandlerFunc(api.createConfig)))
	mux.Handle("GET /api/1/configs/{id}", api.authenticate(http.HandlerFunc(api.getConfig)))
	mux.Handle("PATCH /api/1/configs/{id}", api.authenticate(http.HandlerFunc(api.updateConfig)))
	mux.Handle("DELETE /api/1/configs/{id}", api.authenticate(http.HandlerFunc(api.deleteConfig)))

	return api.recoverPanics(api.requestID(api.securityHeaders(api.accessLog(mux))))
}

func (api *API) root(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":    "tabby-config-sync",
		"version": api.version,
		"status":  "ok",
	})
}

func (api *API) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (api *API) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := api.db.Ping(ctx); err != nil {
		api.logger.Error("readiness check failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "not_ready", "database is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (api *API) getUser(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       user.ID,
		"username": user.Name,
	})
}

func (api *API) listConfigs(w http.ResponseWriter, r *http.Request) {
	configs, err := api.db.ListConfigs(r.Context(), currentUser(r.Context()).ID)
	if err != nil {
		api.internalError(w, r, err)
		return
	}
	response := make([]configResponse, 0, len(configs))
	for _, config := range configs {
		response = append(response, makeConfigResponse(config))
	}
	writeJSON(w, http.StatusOK, response)
}

func (api *API) getConfig(w http.ResponseWriter, r *http.Request) {
	id, ok := parseConfigID(w, r)
	if !ok {
		return
	}
	config, err := api.db.GetConfig(r.Context(), currentUser(r.Context()).ID, id)
	if errors.Is(err, database.ErrConfigNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "config not found")
		return
	}
	if err != nil {
		api.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, makeConfigResponse(config))
}

type createConfigRequest struct {
	Name                *string `json:"name"`
	Content             *string `json:"content"`
	LastUsedWithVersion *string `json:"last_used_with_version"`
}

func (api *API) createConfig(w http.ResponseWriter, r *http.Request) {
	var request createConfigRequest
	if !api.decodeJSON(w, r, &request) {
		return
	}

	name := fmt.Sprintf("Unnamed config (%s)", time.Now().UTC().Format(time.DateOnly))
	if request.Name != nil {
		name = strings.TrimSpace(*request.Name)
	}
	content := "{}"
	if request.Content != nil {
		content = *request.Content
	}
	if err := validateConfigFields(name, content, request.LastUsedWithVersion); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	config, err := api.db.CreateConfig(r.Context(), currentUser(r.Context()).ID, database.NewConfig{
		Name:                name,
		Content:             content,
		LastUsedWithVersion: request.LastUsedWithVersion,
	})
	if err != nil {
		api.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, makeConfigResponse(config))
}

type optionalNullableString struct {
	Set   bool
	Value *string
}

func (value *optionalNullableString) UnmarshalJSON(data []byte) error {
	value.Set = true
	if string(data) == "null" {
		value.Value = nil
		return nil
	}
	var decoded string
	if err := json.Unmarshal(data, &decoded); err != nil {
		return errors.New("must be a string or null")
	}
	value.Value = &decoded
	return nil
}

type updateConfigRequest struct {
	Name                *string                `json:"name"`
	Content             *string                `json:"content"`
	LastUsedWithVersion optionalNullableString `json:"last_used_with_version"`
}

func (api *API) updateConfig(w http.ResponseWriter, r *http.Request) {
	id, ok := parseConfigID(w, r)
	if !ok {
		return
	}
	var request updateConfigRequest
	if !api.decodeJSON(w, r, &request) {
		return
	}
	if request.Name == nil && request.Content == nil && !request.LastUsedWithVersion.Set {
		writeError(w, http.StatusBadRequest, "invalid_request", "at least one field must be provided")
		return
	}
	if request.Name != nil {
		trimmed := strings.TrimSpace(*request.Name)
		request.Name = &trimmed
	}
	if err := validatePatch(request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	config, err := api.db.UpdateConfig(r.Context(), currentUser(r.Context()).ID, id, database.ConfigPatch{
		Name:                request.Name,
		Content:             request.Content,
		VersionSet:          request.LastUsedWithVersion.Set,
		LastUsedWithVersion: request.LastUsedWithVersion.Value,
	})
	if errors.Is(err, database.ErrConfigNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "config not found")
		return
	}
	if err != nil {
		api.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, makeConfigResponse(config))
}

func (api *API) deleteConfig(w http.ResponseWriter, r *http.Request) {
	id, ok := parseConfigID(w, r)
	if !ok {
		return
	}
	err := api.db.DeleteConfig(r.Context(), currentUser(r.Context()).ID, id)
	if errors.Is(err, database.ErrConfigNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "config not found")
		return
	}
	if err != nil {
		api.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			w.Header().Set("WWW-Authenticate", `Bearer realm="tabby-config-sync"`)
			writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
			return
		}
		user, err := api.db.Authenticate(r.Context(), parts[1])
		if errors.Is(err, database.ErrUserNotFound) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="tabby-config-sync"`)
			writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
			return
		}
		if err != nil {
			api.internalError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey{}, user)))
	})
}

func currentUser(ctx context.Context) database.User {
	user, ok := ctx.Value(userContextKey{}).(database.User)
	if !ok {
		panic("authenticated user missing from request context")
	}
	return user
}

func parseConfigID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "not_found", "config not found")
		return 0, false
	}
	return id, true
}

func validateConfigFields(name, content string, version *string) error {
	if name == "" {
		return errors.New("name must not be empty")
	}
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > 255 {
		return errors.New("name must be valid UTF-8 and no longer than 255 characters")
	}
	if !utf8.ValidString(content) {
		return errors.New("content must be valid UTF-8")
	}
	if version != nil && (!utf8.ValidString(*version) || utf8.RuneCountInString(*version) > 32) {
		return errors.New("last_used_with_version must be no longer than 32 characters")
	}
	return nil
}

func validatePatch(request updateConfigRequest) error {
	name := "valid"
	if request.Name != nil {
		name = *request.Name
	}
	content := ""
	if request.Content != nil {
		content = *request.Content
	}
	var version *string
	if request.LastUsedWithVersion.Set {
		version = request.LastUsedWithVersion.Value
	}
	return validateConfigFields(name, content, version)
}

func (api *API) decodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.maxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_json", "request body must be one valid JSON object")
		}
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must contain only one JSON object")
		return false
	}
	return true
}

type configResponse struct {
	ID                  int64   `json:"id"`
	Name                string  `json:"name"`
	Content             string  `json:"content"`
	LastUsedWithVersion *string `json:"last_used_with_version"`
	CreatedAt           string  `json:"created_at"`
	ModifiedAt          string  `json:"modified_at"`
}

func makeConfigResponse(config database.Config) configResponse {
	return configResponse{
		ID:                  config.ID,
		Name:                config.Name,
		Content:             config.Content,
		LastUsedWithVersion: config.LastUsedWithVersion,
		CreatedAt:           formatTime(config.CreatedAt),
		ModifiedAt:          formatTime(config.ModifiedAt),
	}
}

func formatTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
}

func (api *API) internalError(w http.ResponseWriter, r *http.Request, err error) {
	api.logger.Error("request failed",
		"request_id", r.Header.Get("X-Request-ID"),
		"method", r.Method,
		"path", r.URL.Path,
		"error", err,
	)
	writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func (api *API) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := make([]byte, 12)
		if _, err := rand.Read(raw); err != nil {
			api.logger.Error("generate request ID", "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		requestID := hex.EncodeToString(raw)
		w.Header().Set("X-Request-ID", requestID)
		r.Header.Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r)
	})
}

func (api *API) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (recorder *responseRecorder) WriteHeader(status int) {
	if recorder.status != 0 {
		return
	}
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *responseRecorder) Write(data []byte) (int, error) {
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}
	written, err := recorder.ResponseWriter.Write(data)
	recorder.bytes += written
	return written, err
}

func (api *API) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		api.logger.Info("http request",
			"request_id", r.Header.Get("X-Request-ID"),
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"bytes", recorder.bytes,
			"duration_ms", time.Since(started).Milliseconds(),
		)
	})
}

func (api *API) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				api.logger.Error("panic recovered",
					"request_id", r.Header.Get("X-Request-ID"),
					"method", r.Method,
					"path", r.URL.Path,
					"panic", recovered,
				)
				writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
