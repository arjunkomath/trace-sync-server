package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSettingsSyncContract(t *testing.T) {
	s := &server{cfg: config{token: "test-token", dataDir: t.TempDir(), maxBytes: defaultMaxBytes}}

	put := httptest.NewRequest(http.MethodPut, "/v1/settings", strings.NewReader(`{"baseVersion":0,"updatedBy":"test","settings":{"quickLinks":[]}}`))
	put.Header.Set("Authorization", "Bearer test-token")
	put.Header.Set("Content-Type", "application/json")
	putResponse := httptest.NewRecorder()
	s.withAuth(s.handlePutSettings)(putResponse, put)

	if putResponse.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want %d: %s", putResponse.Code, http.StatusOK, putResponse.Body.String())
	}

	var saved settingsResponse
	if err := json.NewDecoder(putResponse.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	if saved.Version != 1 {
		t.Fatalf("version = %d, want 1", saved.Version)
	}

	get := httptest.NewRequest(http.MethodGet, "/v1/settings", nil)
	get.Header.Set("Authorization", "Bearer test-token")
	getResponse := httptest.NewRecorder()
	s.withAuth(s.handleGetSettings)(getResponse, get)

	if getResponse.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d: %s", getResponse.Code, http.StatusOK, getResponse.Body.String())
	}

	var downloaded settingsResponse
	if err := json.NewDecoder(getResponse.Body).Decode(&downloaded); err != nil {
		t.Fatal(err)
	}
	if downloaded.Version != 1 || !bytes.Equal(downloaded.Settings, []byte(`{"quickLinks":[]}`)) {
		t.Fatalf("downloaded = %+v", downloaded)
	}

	conflict := httptest.NewRequest(http.MethodPut, "/v1/settings", strings.NewReader(`{"baseVersion":0,"settings":{"quickLinks":[1]}}`))
	conflict.Header.Set("Authorization", "Bearer test-token")
	conflict.Header.Set("Content-Type", "application/json")
	conflictResponse := httptest.NewRecorder()
	s.withAuth(s.handlePutSettings)(conflictResponse, conflict)

	if conflictResponse.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, want %d: %s", conflictResponse.Code, http.StatusConflict, conflictResponse.Body.String())
	}
}

func TestSettingsWithAmpersandURLStayReadable(t *testing.T) {
	s := &server{cfg: config{token: "test-token", dataDir: t.TempDir(), maxBytes: defaultMaxBytes}}

	put := httptest.NewRequest(http.MethodPut, "/v1/settings", strings.NewReader(`{"baseVersion":0,"settings":{"quickLinks":[{"url":"https://example.com/?a=1&b=2"}]}}`))
	put.Header.Set("Authorization", "Bearer test-token")
	put.Header.Set("Content-Type", "application/json")
	putResponse := httptest.NewRecorder()
	s.withAuth(s.handlePutSettings)(putResponse, put)

	if putResponse.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want %d: %s", putResponse.Code, http.StatusOK, putResponse.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, "/v1/settings", nil)
	get.Header.Set("Authorization", "Bearer test-token")
	getResponse := httptest.NewRecorder()
	s.withAuth(s.handleGetSettings)(getResponse, get)

	if getResponse.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d: %s", getResponse.Code, http.StatusOK, getResponse.Body.String())
	}

	nextPut := httptest.NewRequest(http.MethodPut, "/v1/settings", strings.NewReader(`{"baseVersion":1,"settings":{"quickLinks":[]}}`))
	nextPut.Header.Set("Authorization", "Bearer test-token")
	nextPut.Header.Set("Content-Type", "application/json")
	nextPutResponse := httptest.NewRecorder()
	s.withAuth(s.handlePutSettings)(nextPutResponse, nextPut)

	if nextPutResponse.Code != http.StatusOK {
		t.Fatalf("second PUT status = %d, want %d: %s", nextPutResponse.Code, http.StatusOK, nextPutResponse.Body.String())
	}
}

func TestSettingsRequireBearerToken(t *testing.T) {
	s := &server{cfg: config{token: "test-token", dataDir: t.TempDir(), maxBytes: defaultMaxBytes}}

	req := httptest.NewRequest(http.MethodGet, "/v1/settings", nil)
	res := httptest.NewRecorder()
	s.withAuth(s.handleGetSettings)(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
}

func TestSettingsMustBeObject(t *testing.T) {
	s := &server{cfg: config{token: "test-token", dataDir: t.TempDir(), maxBytes: defaultMaxBytes}}

	req := httptest.NewRequest(http.MethodPut, "/v1/settings", strings.NewReader(`{"baseVersion":0,"settings":null}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	s.withAuth(s.handlePutSettings)(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
}

func TestRejectsTrailingJSON(t *testing.T) {
	s := &server{cfg: config{token: "test-token", dataDir: t.TempDir(), maxBytes: defaultMaxBytes}}

	req := httptest.NewRequest(http.MethodPut, "/v1/settings", strings.NewReader(`{"baseVersion":0,"settings":{}} {}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	s.withAuth(s.handlePutSettings)(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
}
