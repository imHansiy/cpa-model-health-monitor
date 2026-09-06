package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct { void* ptr; size_t len; } cliproxy_buffer;
typedef struct {
    uint32_t abi_version;
    void* host_ctx;
    int (*call)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
    void (*free_buffer)(void*, size_t);
} cliproxy_host_api;
typedef struct {
    uint32_t abi_version;
    int (*call)(char*, uint8_t*, size_t, cliproxy_buffer*);
    void (*free_buffer)(void*, size_t);
    void (*shutdown)(void);
} cliproxy_plugin_api;
typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);
static const cliproxy_host_api* stored_host;
static void store_host_api(const cliproxy_host_api* host) { stored_host = host; }
static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
    if (stored_host == NULL || stored_host->call == NULL) return 1;
    return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}
static void free_host_buffer(void* ptr, size_t len) {
    if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) stored_host->free_buffer(ptr, len);
}
*/
import "C"

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"unsafe"
)

const (
	pluginID   = "cpa-model-health-monitor"
	abiVersion = 1
)

var pluginVersion = "0.1.0"

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

type registration struct {
	SchemaVersion int                  `json:"schema_version"`
	Capabilities  capabilities         `json:"capabilities"`
	Metadata      registrationMetadata `json:"metadata"`
}

type capabilities struct {
	ManagementAPI bool `json:"management_api"`
}

type registrationMetadata struct {
	Name             string        `json:"Name"`
	Version          string        `json:"Version"`
	Author           string        `json:"Author"`
	GitHubRepository string        `json:"GitHubRepository"`
	ConfigFields     []configField `json:"ConfigFields"`
}

type configField struct {
	Name        string `json:"Name"`
	Type        string `json:"Type"`
	Description string `json:"Description,omitempty"`
}

type managementRegistration struct {
	Routes    []managementRoute    `json:"routes"`
	Resources []managementResource `json:"resources"`
}
type managementRoute struct{ Method, Path string }
type managementResource struct{ Path, Menu, Description string }
type managementRequest struct {
	Method         string              `json:"method"`
	Path           string              `json:"path"`
	Headers        map[string][]string `json:"headers,omitempty"`
	Query          map[string][]string `json:"query,omitempty"`
	Body           []byte              `json:"body,omitempty"`
	HostCallbackID string              `json:"host_callback_id,omitempty"`
}
type managementResponse struct {
	StatusCode int                 `json:"StatusCode"`
	Headers    map[string][]string `json:"Headers,omitempty"`
	Body       []byte              `json:"Body,omitempty"`
}

var (
	runtimeMu       sync.RWMutex
	runtimeInstance *Runtime
)

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if host == nil || plugin == nil || uint32(host.abi_version) != abiVersion {
		return 1
	}
	C.store_host_api(host)
	plugin.abi_version = C.uint32_t(abiVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	runtimeMu.Lock()
	if runtimeInstance != nil {
		runtimeInstance.Stop()
	}
	runtimeInstance = NewRuntime(realHost{}, defaultDataDir())
	runtimeMu.Unlock()
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, mustJSON(failure("invalid_method", "method is required")))
		return 1
	}
	raw := []byte("{}")
	if request != nil && requestLen > 0 {
		raw = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	value, err := dispatch(C.GoString(method), raw)
	if err != nil {
		writeResponse(response, mustJSON(failure("plugin_error", err.Error())))
		return 1
	}
	writeResponse(response, mustJSON(success(value)))
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, length C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
	_ = length
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {
	runtimeMu.Lock()
	if runtimeInstance != nil {
		runtimeInstance.Stop()
		runtimeInstance = nil
	}
	runtimeMu.Unlock()
}

func main() {}

func dispatch(method string, raw []byte) (any, error) {
	switch method {
	case "plugin.register", "plugin.reconfigure":
		var req struct {
			ConfigYAML []byte `json:"config_yaml"`
		}
		_ = json.Unmarshal(raw, &req)
		rt := currentRuntime()
		if rt == nil {
			return nil, errors.New("runtime is not initialized")
		}
		if err := rt.Configure(string(req.ConfigYAML)); err != nil {
			return nil, err
		}
		return registrationPayload(), nil
	case "management.register":
		return managementRegistrationPayload(), nil
	case "management.handle":
		var req managementRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("invalid management request: %w", err)
		}
		return handleManagement(req), nil
	default:
		return nil, fmt.Errorf("unsupported method %q", method)
	}
}

func registrationPayload() registration {
	return registration{SchemaVersion: 1, Capabilities: capabilities{ManagementAPI: true}, Metadata: registrationMetadata{
		Name: "CPA Model Health Monitor", Version: pluginVersion, Author: "hansiy",
		GitHubRepository: "https://github.com/imHansiy/cpa-model-health-monitor", ConfigFields: []configField{
			{Name: "interval_min", Type: "integer", Description: "Automatic probe interval in minutes"},
			{Name: "timeout_sec", Type: "integer", Description: "Per-target timeout in seconds"},
			{Name: "failure_threshold", Type: "integer", Description: "Consecutive failures before DOWN"},
			{Name: "recovery_threshold", Type: "integer", Description: "Consecutive successes before UP"},
			{Name: "notify_initial", Type: "boolean", Description: "Send mail for the first observed state"},
		},
	}}
}

func managementRegistrationPayload() managementRegistration {
	base := "/plugins/" + pluginID
	return managementRegistration{Routes: []managementRoute{
		{http.MethodGet, base + "/status"}, {http.MethodGet, base + "/config"}, {http.MethodPut, base + "/config"},
		{http.MethodGet, base + "/auth-files"}, {http.MethodGet, base + "/history"},
		{http.MethodPost, base + "/run"}, {http.MethodPost, base + "/test-email"},
	}, Resources: []managementResource{{Path: "/panel", Menu: "Model Health Monitor", Description: "Configure exact channel, credential and model probes with state-change email alerts."}}}
}

func handleManagement(req managementRequest) managementResponse {
	rt := currentRuntime()
	if rt == nil {
		return jsonResponse(503, map[string]any{"error": "runtime is not initialized"})
	}
	path := normalizePath(req.Path)
	switch {
	case req.Method == http.MethodGet && (path == "/" || path == "/panel" || path == "/index.html" || path == "/0"):
		return managementResponse{StatusCode: 200, Headers: map[string][]string{"Content-Type": {"text/html; charset=utf-8"}, "Cache-Control": {"no-store"}}, Body: []byte(panelHTML)}
	case req.Method == http.MethodGet && path == "/status":
		return jsonResponse(200, rt.Status())
	case req.Method == http.MethodGet && path == "/config":
		return jsonResponse(200, rt.PublicConfig())
	case req.Method == http.MethodPut && path == "/config":
		var cfg Config
		if err := json.Unmarshal(req.Body, &cfg); err != nil {
			return jsonResponse(400, map[string]any{"error": "invalid JSON"})
		}
		if err := rt.UpdateConfig(cfg); err != nil {
			return jsonResponse(400, map[string]any{"error": err.Error()})
		}
		return jsonResponse(200, rt.PublicConfig())
	case req.Method == http.MethodGet && path == "/auth-files":
		files, err := rt.AuthFiles()
		if err != nil {
			return jsonResponse(502, map[string]any{"error": err.Error()})
		}
		return jsonResponse(200, map[string]any{"auth_files": files})
	case req.Method == http.MethodGet && path == "/history":
		return jsonResponse(200, map[string]any{"history": rt.History()})
	case req.Method == http.MethodPost && path == "/run":
		var input struct {
			Wait bool `json:"wait"`
		}
		_ = json.Unmarshal(req.Body, &input)
		done, err := rt.StartRun("manual")
		if errors.Is(err, ErrRunInProgress) {
			return jsonResponse(409, map[string]any{"error": err.Error()})
		}
		if err != nil {
			return jsonResponse(500, map[string]any{"error": err.Error()})
		}
		if input.Wait {
			<-done
			return jsonResponse(200, rt.Status())
		}
		return jsonResponse(202, map[string]any{"started": true})
	case req.Method == http.MethodPost && path == "/test-email":
		if err := rt.TestEmail(); err != nil {
			return jsonResponse(502, map[string]any{"error": err.Error()})
		}
		return jsonResponse(200, map[string]any{"sent": true})
	default:
		return jsonResponse(404, map[string]any{"error": "route not found"})
	}
}

func currentRuntime() *Runtime { runtimeMu.RLock(); defer runtimeMu.RUnlock(); return runtimeInstance }

func normalizePath(raw string) string {
	p := strings.TrimSpace(raw)
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	for _, prefix := range []string{"/v0/resource/plugins/", "/v0/management/plugins/", "/plugins/", "/v0/management/"} {
		if strings.HasPrefix(p, prefix) {
			rest := strings.TrimPrefix(p, prefix)
			if i := strings.Index(rest, "/"); i >= 0 {
				p = rest[i:]
			} else {
				p = "/"
			}
			break
		}
	}
	p = strings.TrimRight(p, "/")
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

func success(v any) envelope { raw, _ := json.Marshal(v); return envelope{OK: true, Result: raw} }
func failure(code, msg string) envelope {
	return envelope{OK: false, Error: &envelopeError{Code: code, Message: msg}}
}
func mustJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		return []byte(`{"ok":false,"error":{"code":"encode_error","message":"encode failed"}}`)
	}
	return raw
}
func writeResponse(resp *C.cliproxy_buffer, raw []byte) {
	if resp == nil || len(raw) == 0 {
		return
	}
	p := C.CBytes(raw)
	if p == nil {
		return
	}
	resp.ptr = p
	resp.len = C.size_t(len(raw))
}
func jsonResponse(status int, v any) managementResponse {
	raw, err := json.Marshal(v)
	if err != nil {
		status = 500
		raw = []byte(`{"error":"encode failed"}`)
	}
	return managementResponse{StatusCode: status, Headers: map[string][]string{"Content-Type": {"application/json; charset=utf-8"}, "Cache-Control": {"no-store"}}, Body: raw}
}

func callHost(method string, request any, result any) error {
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	methodC := C.CString(method)
	defer C.free(unsafe.Pointer(methodC))
	var requestPtr *C.uint8_t
	var requestMem unsafe.Pointer
	if len(raw) > 0 {
		requestMem = C.CBytes(raw)
		if requestMem == nil {
			return errors.New("allocate host request")
		}
		defer C.free(requestMem)
		requestPtr = (*C.uint8_t)(requestMem)
	}
	var response C.cliproxy_buffer
	code := C.call_host_api(methodC, requestPtr, C.size_t(len(raw)), &response)
	var responseRaw []byte
	if response.ptr != nil && response.len > 0 {
		responseRaw = C.GoBytes(response.ptr, C.int(response.len))
	}
	if response.ptr != nil {
		C.free_host_buffer(response.ptr, response.len)
	}
	if len(responseRaw) == 0 {
		return fmt.Errorf("host callback %s returned no response", method)
	}
	var env envelope
	if err := json.Unmarshal(responseRaw, &env); err != nil {
		return fmt.Errorf("decode host callback: %w", err)
	}
	if !env.OK {
		if env.Error != nil {
			return errors.New(env.Error.Message)
		}
		return errors.New("host callback failed")
	}
	if code != 0 {
		return fmt.Errorf("host callback failed with code %d", int(code))
	}
	if result == nil || len(env.Result) == 0 {
		return nil
	}
	return json.Unmarshal(env.Result, result)
}
