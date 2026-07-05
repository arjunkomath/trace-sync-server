package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultPort     = "8787"
	defaultDataDir  = "./data"
	defaultMaxBytes = int64(1024 * 1024)
)

type config struct {
	token    string
	dataDir  string
	port     string
	maxBytes int64
}

type server struct {
	cfg config
	mu  sync.Mutex
}

type state struct {
	Version   int             `json:"version"`
	UpdatedAt string          `json:"updatedAt"`
	UpdatedBy string          `json:"updatedBy,omitempty"`
	SHA256    string          `json:"sha256"`
	Settings  json.RawMessage `json:"settings"`
}

type settingsResponse struct {
	Version   int             `json:"version"`
	UpdatedAt string          `json:"updatedAt"`
	UpdatedBy string          `json:"updatedBy,omitempty"`
	SHA256    string          `json:"sha256"`
	Settings  json.RawMessage `json:"settings"`
}

type putSettingsRequest struct {
	BaseVersion int             `json:"baseVersion"`
	UpdatedBy   string          `json:"updatedBy,omitempty"`
	Settings    json.RawMessage `json:"settings"`
}

type errorResponse struct {
	Error          string `json:"error"`
	CurrentVersion int    `json:"currentVersion,omitempty"`
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	if err := os.MkdirAll(cfg.dataDir, 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	s := &server{cfg: cfg}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /v1/settings", s.withAuth(s.handleGetSettings))
	mux.HandleFunc("PUT /v1/settings", s.withAuth(s.handlePutSettings))

	addr := ":" + cfg.port
	log.Printf("trace-sync-server listening on %s, data dir %s", addr, cfg.dataDir)
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func loadConfig() (config, error) {
	token := strings.TrimSpace(os.Getenv("TRACE_SYNC_TOKEN"))
	if token == "" {
		return config{}, errors.New("TRACE_SYNC_TOKEN is required")
	}

	cfg := config{
		token:    token,
		dataDir:  envOrDefault("TRACE_SYNC_DATA_DIR", defaultDataDir),
		port:     envOrDefault("TRACE_SYNC_PORT", defaultPort),
		maxBytes: defaultMaxBytes,
	}

	if rawMaxBytes := strings.TrimSpace(os.Getenv("TRACE_SYNC_MAX_BYTES")); rawMaxBytes != "" {
		maxBytes, err := strconv.ParseInt(rawMaxBytes, 10, 64)
		if err != nil || maxBytes <= 0 {
			return config{}, fmt.Errorf("TRACE_SYNC_MAX_BYTES must be a positive integer")
		}
		cfg.maxBytes = maxBytes
	}

	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	// NOTE: a single-process mutex is enough for the official one-binary deployment.
	// If multiple server processes ever share a volume, switch to SQLite or OS file locks.
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.readState()
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "not_found"})
		return
	}
	if err != nil {
		log.Printf("read settings: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "read_failed"})
		return
	}

	writeJSON(w, http.StatusOK, settingsResponse{
		Version:   state.Version,
		UpdatedAt: state.UpdatedAt,
		UpdatedBy: state.UpdatedBy,
		SHA256:    state.SHA256,
		Settings:  state.Settings,
	})
}

func (s *server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.maxBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request_too_large"})
		return
	}

	var req putSettingsRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid_json"})
		return
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid_json"})
		return
	}
	if len(req.Settings) == 0 || !json.Valid(req.Settings) || !isJSONObject(req.Settings) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid_settings"})
		return
	}
	if req.BaseVersion < 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid_base_version"})
		return
	}

	// NOTE: a single-process mutex is enough for the official one-binary deployment.
	// If multiple server processes ever share a volume, switch to SQLite or OS file locks.
	s.mu.Lock()
	defer s.mu.Unlock()

	currentState, err := s.readState()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("read settings: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "read_failed"})
		return
	}

	currentVersion := 0
	if err == nil {
		currentVersion = currentState.Version
	}

	if req.BaseVersion != currentVersion {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "conflict", CurrentVersion: currentVersion})
		return
	}

	nextState := state{
		Version:   currentVersion + 1,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedBy: strings.TrimSpace(req.UpdatedBy),
		SHA256:    sha256Hex(req.Settings),
		Settings:  req.Settings,
	}

	if err := s.writeState(nextState); err != nil {
		log.Printf("write settings: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "write_failed"})
		return
	}

	writeJSON(w, http.StatusOK, settingsResponse{
		Version:   nextState.Version,
		UpdatedAt: nextState.UpdatedAt,
		UpdatedBy: nextState.UpdatedBy,
		SHA256:    nextState.SHA256,
		Settings:  nextState.Settings,
	})
}

func (s *server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		provided, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(provided), []byte(s.cfg.token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
			return
		}
		next(w, r)
	}
}

func (s *server) readState() (state, error) {
	data, err := os.ReadFile(s.statePath())
	if err != nil {
		return state{}, err
	}

	var state state
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	if state.Version <= 0 || !json.Valid(state.Settings) || !isJSONObject(state.Settings) || state.SHA256 != sha256Hex(state.Settings) {
		return state, errors.New("state file is invalid")
	}
	return state, nil
}

func (s *server) writeState(state state) error {
	var stateData bytes.Buffer
	encoder := json.NewEncoder(&stateData)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(state); err != nil {
		return err
	}
	return writeFileAtomic(s.statePath(), stateData.Bytes(), 0o600)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmpPath, path)
}

func (s *server) statePath() string {
	return filepath.Join(s.cfg.dataDir, "state.json")
}

func sha256Hex(data []byte) string {
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, data); err == nil {
		data = compacted.Bytes()
	}

	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func rejectTrailingJSON(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}

func isJSONObject(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write response: %v", err)
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		log.Printf("%s %s -> %d (%s)", r.Method, r.URL.Path, recorder.status, time.Since(start).Round(time.Millisecond))
	})
}
