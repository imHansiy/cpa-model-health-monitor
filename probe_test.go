package main

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type fakeHost struct {
	mu               sync.Mutex
	auth             map[string]json.RawMessage
	getAuthIndexes   []string
	requests         []HostHTTPRequest
	response         HostHTTPResponse
	err              error
	echoVerification bool
}

func (f *fakeHost) ListAuthFiles(context.Context) ([]AuthFile, error) {
	return []AuthFile{{AuthIndex: "auth-1", Provider: "codex", Email: "u@example.com"}}, nil
}
func (f *fakeHost) GetAuth(_ context.Context, index string) (json.RawMessage, error) {
	f.mu.Lock()
	f.getAuthIndexes = append(f.getAuthIndexes, index)
	f.mu.Unlock()
	return f.auth[index], nil
}
func (f *fakeHost) HTTPDo(_ context.Context, req HostHTTPRequest) (HostHTTPResponse, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	if f.echoVerification {
		var body map[string]any
		_ = json.Unmarshal(req.Body, &body)
		messages, _ := body["messages"].([]any)
		if len(messages) > 0 {
			message, _ := messages[0].(map[string]any)
			prompt, _ := message["content"].(string)
			if marker := strings.LastIndex(prompt, "CPA_CHECK="); marker >= 0 {
				value := strings.TrimSuffix(prompt[marker:], " and nothing else.")
				f.response = HostHTTPResponse{StatusCode: 200, Body: []byte(`{"choices":[{"message":{"content":` + strconv.Quote(value) + `}}]}`)}
			}
		}
	}
	response := f.response
	f.mu.Unlock()
	return response, f.err
}
func (f *fakeHost) Log(context.Context, string, string, map[string]any) {}

func TestProbePinsSelectedCPAAuthAndValidatesOutput(t *testing.T) {
	host := &fakeHost{auth: map[string]json.RawMessage{"auth-1": json.RawMessage(`{"access_token":"selected-secret"}`)}, echoVerification: true}
	target := Target{ID: "t1", Name: "test", Enabled: true, Source: "cpa_auth", AuthIndex: "auth-1", Protocol: "openai_chat", BaseURL: "https://api.example.com/v1", Model: "model", AuthMode: "bearer"}
	runtime := NewRuntime(host, t.TempDir())
	result := runtime.probeTarget(context.Background(), target, 5)
	if !result.Healthy {
		t.Fatalf("probe failed: %+v", result)
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if len(host.getAuthIndexes) != 1 || host.getAuthIndexes[0] != "auth-1" {
		t.Fatalf("auth indexes = %v", host.getAuthIndexes)
	}
	if len(host.requests) != 1 || host.requests[0].Headers["Authorization"][0] != "Bearer selected-secret" {
		t.Fatal("selected credential was not applied")
	}
}

func TestProtocolResponseParsers(t *testing.T) {
	tests := []struct{ protocol, body, want string }{{"openai_chat", `{"choices":[{"message":{"content":"CPA_CHECK=7"}}]}`, "CPA_CHECK=7"}, {"openai_responses", `{"output":[{"type":"message","content":[{"type":"output_text","text":"CPA_CHECK=8"}]}]}`, "CPA_CHECK=8"}, {"anthropic_messages", `{"content":[{"type":"text","text":"CPA_CHECK=9"}]}`, "CPA_CHECK=9"}, {"gemini_generate", `{"candidates":[{"content":{"parts":[{"text":"CPA_CHECK=10"}]}}]}`, "CPA_CHECK=10"}, {"codex_responses", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"CPA_CHECK=11\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", "CPA_CHECK=11"}}
	for _, tt := range tests {
		t.Run(tt.protocol, func(t *testing.T) {
			got, err := parseProbeResponse(tt.protocol, []byte(tt.body))
			if err != nil || strings.TrimSpace(got) != tt.want {
				t.Fatalf("got %q err=%v", got, err)
			}
		})
	}
}

func TestEndpointJoining(t *testing.T) {
	if got := endpointURL("https://api.example.com/v1", "/v1/chat/completions", "/chat/completions"); got != "https://api.example.com/v1/chat/completions" {
		t.Fatal(got)
	}
	if got := endpointURL("https://api.example.com/v1/chat/completions", "/v1/chat/completions", "/chat/completions"); got != "https://api.example.com/v1/chat/completions" {
		t.Fatal(got)
	}
}
