package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestManagementSettingsAvoidsHostConfigRouteAndRedactsSecrets(t *testing.T) {
	rt := NewRuntime(&fakeHost{}, t.TempDir())
	runtimeMu.Lock()
	previous := runtimeInstance
	runtimeInstance = rt
	runtimeMu.Unlock()
	t.Cleanup(func() {
		rt.Stop()
		runtimeMu.Lock()
		runtimeInstance = previous
		runtimeMu.Unlock()
	})

	payload := Config{
		Enabled: true, IntervalMin: 30, TimeoutSec: 5, FailureThreshold: 1, RecoveryThreshold: 1, MaxConcurrency: 2,
		SMTP:    SMTPConfig{Port: 587, TLSMode: "starttls"},
		Targets: []Target{{ID: "one", Enabled: true, Source: "direct", Protocol: "openai_chat", BaseURL: "https://example.com/v1", Model: "model", APIKey: "test-secret", AuthMode: "bearer"}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	put := handleManagement(managementRequest{Method: http.MethodPut, Path: "/settings", Body: body})
	if put.StatusCode != http.StatusOK {
		t.Fatalf("PUT /settings status = %d body=%s", put.StatusCode, put.Body)
	}
	get := handleManagement(managementRequest{Method: http.MethodGet, Path: "/settings"})
	if get.StatusCode != http.StatusOK {
		t.Fatalf("GET /settings status = %d body=%s", get.StatusCode, get.Body)
	}
	if strings.Contains(string(get.Body), "test-secret") {
		t.Fatal("GET /settings exposed target API key")
	}
	if !strings.Contains(string(get.Body), `"target_secret_set":{"one":true}`) {
		t.Fatalf("GET /settings did not report retained secret: %s", get.Body)
	}
	if response := handleManagement(managementRequest{Method: http.MethodGet, Path: "/config"}); response.StatusCode != http.StatusNotFound {
		t.Fatalf("plugin must not register the host-reserved /config route, got %d", response.StatusCode)
	}
	for _, route := range managementRegistrationPayload().Routes {
		if strings.HasSuffix(route.Path, "/config") {
			t.Fatalf("registered host-reserved route %q", route.Path)
		}
	}
}
