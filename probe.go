package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type credentialMaterial struct{ Token, AccountID, BaseURL string }

func (r *Runtime) probeTarget(parent context.Context, t Target, timeoutSec int) ProbeResult {
	start := time.Now()
	result := ProbeResult{TargetID: t.ID, Name: t.Name, Model: t.Model, Status: "checking", CheckedAt: start.UTC()}
	finish := func(healthy bool, status, code, message string, httpStatus int) ProbeResult {
		result.Healthy = healthy
		result.Status = status
		result.ErrorCode = code
		result.Error = message
		result.HTTPStatus = httpStatus
		result.LatencyMS = time.Since(start).Milliseconds()
		return result
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeoutSec)*time.Second)
	defer cancel()
	material := credentialMaterial{Token: t.APIKey, BaseURL: t.BaseURL}
	if t.Source == "cpa_auth" {
		raw, err := r.host.GetAuth(ctx, t.AuthIndex)
		if err != nil {
			return finish(false, "credential_error", "credential_read_failed", "CPA could not read the selected credential", 0)
		}
		material, err = materialFromJSON(raw, t)
		if err != nil {
			return finish(false, "credential_error", "credential_invalid", err.Error(), 0)
		}
	}
	if material.BaseURL == "" {
		if t.Protocol == "codex_responses" {
			material.BaseURL = "https://chatgpt.com/backend-api/codex"
		} else {
			return finish(false, "config_error", "missing_base_url", "No base URL is configured", 0)
		}
	}
	if t.AuthMode != "none" && material.Token == "" {
		return finish(false, "credential_error", "missing_token", "The selected credential does not contain a usable token", 0)
	}
	now := time.Now().UnixNano()
	a := int(now%71) + 11
	b := int((now/97)%67) + 13
	expected := "CPA_CHECK=" + strconv.Itoa(a+b)
	prompt := fmt.Sprintf("Add %d and %d. Reply with exactly %s and nothing else.", a, b, expected)
	req, err := buildProbeRequest(t, material, prompt)
	if err != nil {
		return finish(false, "config_error", "request_build_failed", err.Error(), 0)
	}
	type outcome struct {
		resp HostHTTPResponse
		err  error
	}
	ch := make(chan outcome, 1)
	go func() { resp, e := r.host.HTTPDo(ctx, req); ch <- outcome{resp, e} }()
	var resp HostHTTPResponse
	select {
	case <-ctx.Done():
		return finish(false, "timeout", "timeout", "The probe timed out", 0)
	case out := <-ch:
		if out.err != nil {
			return finish(false, "network_error", "network_error", "CPA could not reach the configured upstream", 0)
		}
		resp = out.resp
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return finish(false, classifyHTTP(resp.StatusCode), classifyHTTP(resp.StatusCode), fmt.Sprintf("Upstream returned HTTP %d", resp.StatusCode), resp.StatusCode)
	}
	text, err := parseProbeResponse(t.Protocol, resp.Body)
	if err != nil {
		return finish(false, "response_error", "response_format_error", err.Error(), resp.StatusCode)
	}
	if strings.TrimSpace(text) != expected {
		return finish(false, "response_error", "unexpected_output", "Model output did not match the verification value", resp.StatusCode)
	}
	return finish(true, "healthy", "", "", resp.StatusCode)
}

func materialFromJSON(raw []byte, t Target) (credentialMaterial, error) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return credentialMaterial{}, errors.New("credential JSON is invalid")
	}
	m := credentialMaterial{BaseURL: t.BaseURL}
	paths := []string{t.TokenPath, "access_token", "api_key", "token", "key", "credentials.access_token", "metadata.access_token"}
	for _, p := range paths {
		if p != "" {
			if v, ok := jsonStringPath(doc, p); ok && strings.TrimSpace(v) != "" {
				m.Token = strings.TrimSpace(v)
				break
			}
		}
	}
	accountPaths := []string{t.AccountIDPath, "account_id", "account.id", "metadata.account_id"}
	for _, p := range accountPaths {
		if p != "" {
			if v, ok := jsonStringPath(doc, p); ok {
				m.AccountID = v
				break
			}
		}
	}
	if m.BaseURL == "" {
		for _, p := range []string{"base_url", "base-url", "url", "endpoint"} {
			if v, ok := jsonStringPath(doc, p); ok {
				m.BaseURL = strings.TrimRight(v, "/")
				break
			}
		}
	}
	return m, nil
}

func jsonStringPath(doc any, path string) (string, bool) {
	current := doc
	for _, part := range strings.Split(path, ".") {
		m, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		current, ok = m[part]
		if !ok {
			return "", false
		}
	}
	switch v := current.(type) {
	case string:
		return v, true
	case json.Number:
		return v.String(), true
	default:
		return "", false
	}
}

func buildProbeRequest(t Target, m credentialMaterial, prompt string) (HostHTTPRequest, error) {
	headers := map[string][]string{"Content-Type": {"application/json"}, "Accept": {"application/json"}}
	for k, v := range t.Headers {
		if strings.TrimSpace(k) != "" {
			headers[k] = []string{v}
		}
	}
	applyAuth(headers, t.AuthMode, m.Token)
	var body []byte
	var endpoint string
	var err error
	switch t.Protocol {
	case "openai_chat":
		endpoint = endpointURL(m.BaseURL, "/v1/chat/completions", "/chat/completions")
		body, err = json.Marshal(map[string]any{"model": t.Model, "messages": []any{map[string]any{"role": "user", "content": prompt}}, "max_tokens": 24, "temperature": 0})
	case "openai_responses":
		endpoint = endpointURL(m.BaseURL, "/v1/responses", "/responses")
		body, err = json.Marshal(map[string]any{"model": t.Model, "input": prompt, "max_output_tokens": 24, "stream": false})
	case "anthropic_messages":
		endpoint = endpointURL(m.BaseURL, "/v1/messages", "/messages")
		if _, ok := headers["anthropic-version"]; !ok {
			headers["anthropic-version"] = []string{"2023-06-01"}
		}
		body, err = json.Marshal(map[string]any{"model": t.Model, "max_tokens": 24, "temperature": 0, "messages": []any{map[string]any{"role": "user", "content": prompt}}})
	case "gemini_generate":
		endpoint = geminiEndpoint(m.BaseURL, t.Model)
		body, err = json.Marshal(map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": prompt}}}}, "generationConfig": map[string]any{"temperature": 0, "maxOutputTokens": 24}})
	case "codex_responses":
		endpoint = endpointURL(m.BaseURL, "/responses", "/responses")
		headers["Accept"] = []string{"text/event-stream"}
		headers["Originator"] = []string{"codex-tui"}
		headers["User-Agent"] = []string{fmt.Sprintf("codex-tui/0.146.0 (Linux; %s)", runtime.GOARCH)}
		if m.AccountID != "" {
			headers["Chatgpt-Account-Id"] = []string{m.AccountID}
		}
		body, err = json.Marshal(map[string]any{"model": t.Model, "instructions": "Return only the requested verification value.", "input": []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": prompt}}}}, "stream": true, "store": false, "reasoning": map[string]string{"effort": "low"}})
	default:
		return HostHTTPRequest{}, errors.New("unsupported protocol")
	}
	if err != nil {
		return HostHTTPRequest{}, err
	}
	if t.AuthMode == "query-key" {
		u, e := url.Parse(endpoint)
		if e != nil {
			return HostHTTPRequest{}, e
		}
		q := u.Query()
		q.Set("key", m.Token)
		u.RawQuery = q.Encode()
		endpoint = u.String()
	}
	return HostHTTPRequest{Method: http.MethodPost, URL: endpoint, Headers: headers, Body: body}, nil
}

func applyAuth(headers map[string][]string, mode, token string) {
	switch mode {
	case "bearer":
		headers["Authorization"] = []string{"Bearer " + token}
	case "x-api-key":
		headers["x-api-key"] = []string{token}
	}
}

func endpointURL(base, withV1, withoutV1 string) string {
	base = strings.TrimRight(base, "/")
	for _, suffix := range []string{withV1, withoutV1} {
		if strings.HasSuffix(base, suffix) {
			return base
		}
	}
	if strings.HasSuffix(base, "/v1") {
		return base + withoutV1
	}
	return base + withV1
}
func geminiEndpoint(base, model string) string {
	base = strings.TrimRight(base, "/")
	if strings.Contains(base, ":generateContent") {
		return base
	}
	if strings.HasSuffix(base, "/v1beta") {
		return base + "/models/" + url.PathEscape(model) + ":generateContent"
	}
	return base + "/v1beta/models/" + url.PathEscape(model) + ":generateContent"
}

func classifyHTTP(code int) string {
	switch code {
	case 401:
		return "unauthorized"
	case 402:
		return "payment_required"
	case 403:
		return "forbidden"
	case 404:
		return "not_found"
	case 429:
		return "rate_limited"
	}
	if code >= 500 {
		return "upstream_error"
	}
	return "request_error"
}

func parseProbeResponse(protocol string, body []byte) (string, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return "", errors.New("upstream returned an empty body")
	}
	switch protocol {
	case "openai_chat":
		var v struct {
			Choices []struct {
				Message struct {
					Content any `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if json.Unmarshal(body, &v) != nil || len(v.Choices) == 0 {
			return "", errors.New("invalid OpenAI chat response")
		}
		return contentValue(v.Choices[0].Message.Content), nil
	case "openai_responses":
		var v any
		if json.Unmarshal(body, &v) != nil {
			return "", errors.New("invalid OpenAI Responses response")
		}
		if object, ok := v.(map[string]any); ok {
			if text, ok := object["output_text"].(string); ok && text != "" {
				return text, nil
			}
		}
		text := extractText(v)
		if text == "" {
			return "", errors.New("OpenAI response has no output text")
		}
		return text, nil
	case "anthropic_messages":
		var v struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(body, &v) != nil {
			return "", errors.New("invalid Anthropic response")
		}
		var out strings.Builder
		for _, p := range v.Content {
			if p.Type == "text" || p.Type == "" {
				out.WriteString(p.Text)
			}
		}
		if out.Len() == 0 {
			return "", errors.New("Anthropic response has no text")
		}
		return out.String(), nil
	case "gemini_generate":
		var v struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if json.Unmarshal(body, &v) != nil || len(v.Candidates) == 0 {
			return "", errors.New("invalid Gemini response")
		}
		var out strings.Builder
		for _, p := range v.Candidates[0].Content.Parts {
			out.WriteString(p.Text)
		}
		if out.Len() == 0 {
			return "", errors.New("Gemini response has no text")
		}
		return out.String(), nil
	case "codex_responses":
		return parseCodexCompleted(body)
	}
	return "", errors.New("unsupported response protocol")
}

func contentValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		var b strings.Builder
		for _, item := range x {
			if m, ok := item.(map[string]any); ok {
				if text, ok := m["text"].(string); ok {
					b.WriteString(text)
				}
			}
		}
		return b.String()
	}
	return ""
}

func extractText(v any) string {
	var b strings.Builder
	var walk func(any)
	walk = func(x any) {
		switch item := x.(type) {
		case []any:
			for _, child := range item {
				walk(child)
			}
		case map[string]any:
			if text, ok := item["output_text"].(string); ok {
				b.WriteString(text)
			}
			if item["type"] == "output_text" {
				if text, ok := item["text"].(string); ok {
					b.WriteString(text)
				}
			}
			for k, child := range item {
				if k != "output_text" && k != "text" {
					walk(child)
				}
			}
		}
	}
	walk(v)
	return b.String()
}

func parseCodexCompleted(body []byte) (string, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var v map[string]any
		if json.Unmarshal(trimmed, &v) != nil || v["type"] != "response.completed" {
			return "", errors.New("Codex response did not complete")
		}
		text := extractText(v)
		if text == "" {
			return "", errors.New("Codex response has no output text")
		}
		return text, nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var delta strings.Builder
	completed := false
	failed := false
	terminal := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event map[string]any
		if json.Unmarshal([]byte(data), &event) != nil {
			return "", errors.New("invalid Codex SSE event")
		}
		switch event["type"] {
		case "response.output_text.delta":
			if v, ok := event["delta"].(string); ok {
				delta.WriteString(v)
			}
		case "response.completed":
			completed = true
			terminal = extractText(event)
		case "response.failed", "response.incomplete", "error":
			failed = true
		}
	}
	if scanner.Err() != nil {
		return "", errors.New("unable to read Codex SSE")
	}
	if failed || !completed {
		return "", errors.New("Codex response did not complete")
	}
	if terminal != "" {
		return terminal, nil
	}
	if delta.Len() == 0 {
		return "", errors.New("Codex response has no output text")
	}
	return delta.String(), nil
}
