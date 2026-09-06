package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type Config struct {
	Enabled           bool       `json:"enabled"`
	IntervalMin       int        `json:"interval_min"`
	TimeoutSec        int        `json:"timeout_sec"`
	FailureThreshold  int        `json:"failure_threshold"`
	RecoveryThreshold int        `json:"recovery_threshold"`
	NotifyInitial     bool       `json:"notify_initial"`
	MaxConcurrency    int        `json:"max_concurrency"`
	SMTP              SMTPConfig `json:"smtp"`
	Targets           []Target   `json:"targets"`
}

type SMTPConfig struct {
	Enabled  bool     `json:"enabled"`
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	TLSMode  string   `json:"tls_mode"`
	Username string   `json:"username"`
	Password string   `json:"password,omitempty"`
	From     string   `json:"from"`
	To       []string `json:"to"`
}

type Target struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Enabled       bool              `json:"enabled"`
	CheckType     string            `json:"check_type,omitempty"`
	Source        string            `json:"source"`
	AuthIndex     string            `json:"auth_index,omitempty"`
	Protocol      string            `json:"protocol"`
	BaseURL       string            `json:"base_url"`
	Model         string            `json:"model"`
	APIKey        string            `json:"api_key,omitempty"`
	TokenPath     string            `json:"token_path,omitempty"`
	AccountIDPath string            `json:"account_id_path,omitempty"`
	AuthMode      string            `json:"auth_mode,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
}

type PublicConfig struct {
	Config
	SMTPPasswordSet bool            `json:"smtp_password_set"`
	TargetSecretSet map[string]bool `json:"target_secret_set"`
}

func defaultConfig() Config {
	return Config{Enabled: true, IntervalMin: 30, TimeoutSec: 30, FailureThreshold: 1, RecoveryThreshold: 1, MaxConcurrency: 4,
		SMTP: SMTPConfig{Port: 587, TLSMode: "starttls"}, Targets: []Target{}}
}

func normalizeConfig(c Config) (Config, error) {
	if c.IntervalMin == 0 {
		c.IntervalMin = 30
	}
	if c.IntervalMin < 1 || c.IntervalMin > 10080 {
		return c, errors.New("interval_min must be between 1 and 10080")
	}
	if c.TimeoutSec == 0 {
		c.TimeoutSec = 30
	}
	if c.TimeoutSec < 3 || c.TimeoutSec > 300 {
		return c, errors.New("timeout_sec must be between 3 and 300")
	}
	if c.FailureThreshold == 0 {
		c.FailureThreshold = 1
	}
	if c.FailureThreshold < 1 || c.FailureThreshold > 20 {
		return c, errors.New("failure_threshold must be between 1 and 20")
	}
	if c.RecoveryThreshold == 0 {
		c.RecoveryThreshold = 1
	}
	if c.RecoveryThreshold < 1 || c.RecoveryThreshold > 20 {
		return c, errors.New("recovery_threshold must be between 1 and 20")
	}
	if c.MaxConcurrency == 0 {
		c.MaxConcurrency = 4
	}
	if c.MaxConcurrency < 1 || c.MaxConcurrency > 32 {
		return c, errors.New("max_concurrency must be between 1 and 32")
	}
	c.SMTP.Host = strings.TrimSpace(c.SMTP.Host)
	c.SMTP.Username = strings.TrimSpace(c.SMTP.Username)
	c.SMTP.From = strings.TrimSpace(c.SMTP.From)
	if c.SMTP.Port == 0 {
		c.SMTP.Port = 587
	}
	c.SMTP.TLSMode = strings.ToLower(strings.TrimSpace(c.SMTP.TLSMode))
	if c.SMTP.TLSMode == "" {
		c.SMTP.TLSMode = "starttls"
	}
	if c.SMTP.TLSMode != "starttls" && c.SMTP.TLSMode != "implicit" && c.SMTP.TLSMode != "none" {
		return c, errors.New("smtp.tls_mode must be starttls, implicit, or none")
	}
	cleanTo := make([]string, 0, len(c.SMTP.To))
	for _, v := range c.SMTP.To {
		v = strings.TrimSpace(v)
		if v != "" {
			cleanTo = append(cleanTo, v)
		}
	}
	c.SMTP.To = cleanTo
	if c.SMTP.Enabled {
		if c.SMTP.Host == "" || c.SMTP.From == "" || len(c.SMTP.To) == 0 {
			return c, errors.New("enabled SMTP requires host, from, and at least one recipient")
		}
	}
	ids := map[string]bool{}
	for i := range c.Targets {
		t := &c.Targets[i]
		t.ID = strings.TrimSpace(t.ID)
		t.Name = strings.TrimSpace(t.Name)
		t.CheckType = strings.ToLower(strings.TrimSpace(t.CheckType))
		if t.CheckType == "" {
			t.CheckType = "model"
		}
		if t.CheckType != "provider" && t.CheckType != "credential" && t.CheckType != "model" {
			return c, fmt.Errorf("target %s check_type must be provider, credential, or model", t.ID)
		}
		t.Source = strings.ToLower(strings.TrimSpace(t.Source))
		t.Protocol = strings.ToLower(strings.TrimSpace(t.Protocol))
		t.BaseURL = strings.TrimRight(strings.TrimSpace(t.BaseURL), "/")
		t.Model = strings.TrimSpace(t.Model)
		t.AuthIndex = strings.TrimSpace(t.AuthIndex)
		t.TokenPath = strings.TrimSpace(t.TokenPath)
		t.AccountIDPath = strings.TrimSpace(t.AccountIDPath)
		t.AuthMode = strings.ToLower(strings.TrimSpace(t.AuthMode))
		if t.ID == "" {
			return c, fmt.Errorf("target %d requires id", i+1)
		}
		if ids[t.ID] {
			return c, fmt.Errorf("duplicate target id %q", t.ID)
		}
		ids[t.ID] = true
		if t.Name == "" {
			t.Name = t.ID
		}
		if t.CheckType == "provider" {
			t.Source = "direct"
			t.AuthMode = "none"
		} else if t.Source == "" {
			t.Source = "direct"
		}
		if t.Source != "direct" && t.Source != "cpa_auth" {
			return c, fmt.Errorf("target %s source must be direct or cpa_auth", t.ID)
		}
		if t.CheckType != "provider" && t.Source == "cpa_auth" && t.AuthIndex == "" {
			return c, fmt.Errorf("target %s requires auth_index", t.ID)
		}
		if t.CheckType == "provider" {
			if t.BaseURL == "" {
				return c, fmt.Errorf("target %s provider check requires base_url", t.ID)
			}
			if _, err := validateHTTPURL(t.BaseURL); err != nil {
				return c, fmt.Errorf("target %s base_url: %w", t.ID, err)
			}
			continue
		}
		if t.CheckType == "credential" {
			continue
		}
		if t.Model == "" {
			return c, fmt.Errorf("target %s requires model", t.ID)
		}
		switch t.Protocol {
		case "openai_chat", "openai_responses", "anthropic_messages", "gemini_generate", "codex_responses":
		default:
			return c, fmt.Errorf("target %s has unsupported protocol", t.ID)
		}
		if t.BaseURL == "" && t.Protocol != "codex_responses" {
			return c, fmt.Errorf("target %s requires base_url", t.ID)
		}
		if t.BaseURL != "" {
			u, err := url.Parse(t.BaseURL)
			if err != nil || u.Scheme == "" || u.Host == "" {
				return c, fmt.Errorf("target %s has invalid base_url", t.ID)
			}
			if u.Scheme != "http" && u.Scheme != "https" {
				return c, fmt.Errorf("target %s base_url must use http or https", t.ID)
			}
		}
		if t.AuthMode == "" {
			switch t.Protocol {
			case "anthropic_messages":
				t.AuthMode = "x-api-key"
			case "gemini_generate":
				t.AuthMode = "query-key"
			default:
				t.AuthMode = "bearer"
			}
		}
		if t.AuthMode != "bearer" && t.AuthMode != "x-api-key" && t.AuthMode != "query-key" && t.AuthMode != "none" {
			return c, fmt.Errorf("target %s has unsupported auth_mode", t.ID)
		}
		for key := range t.Headers {
			normalizedKey := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "_", "-"))
			if normalizedKey == "authorization" || strings.Contains(normalizedKey, "api-key") || strings.Contains(normalizedKey, "token") || strings.Contains(normalizedKey, "secret") {
				return c, fmt.Errorf("target %s contains a sensitive extra header %q; use the API key field and auth mode instead", t.ID, key)
			}
		}
	}
	return c, nil
}

func validateHTTPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, errors.New("invalid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("URL must use http or https")
	}
	return u, nil
}

func mergeSecrets(next, old Config) Config {
	if next.SMTP.Password == "" {
		next.SMTP.Password = old.SMTP.Password
	}
	oldTargets := map[string]Target{}
	for _, t := range old.Targets {
		oldTargets[t.ID] = t
	}
	for i := range next.Targets {
		if next.Targets[i].APIKey == "" {
			if previous, ok := oldTargets[next.Targets[i].ID]; ok {
				next.Targets[i].APIKey = previous.APIKey
			}
		}
	}
	return next
}

func publicConfig(c Config) PublicConfig {
	// Config is passed by value, but slices and maps still share their backing
	// storage. Clone the nested values before redacting so a management read can
	// never erase credentials from the live runtime configuration.
	c.SMTP.To = append([]string(nil), c.SMTP.To...)
	c.Targets = append([]Target(nil), c.Targets...)
	for i := range c.Targets {
		if c.Targets[i].Headers != nil {
			headers := make(map[string]string, len(c.Targets[i].Headers))
			for key, value := range c.Targets[i].Headers {
				headers[key] = value
			}
			c.Targets[i].Headers = headers
		}
	}
	secretSet := map[string]bool{}
	for i := range c.Targets {
		secretSet[c.Targets[i].ID] = c.Targets[i].APIKey != ""
		c.Targets[i].APIKey = ""
	}
	passwordSet := c.SMTP.Password != ""
	c.SMTP.Password = ""
	return PublicConfig{Config: c, SMTPPasswordSet: passwordSet, TargetSecretSet: secretSet}
}

func parseBootstrap(raw string, base Config) Config {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), "\"'")
		switch key {
		case "interval_min":
			base.IntervalMin, _ = strconv.Atoi(value)
		case "timeout_sec":
			base.TimeoutSec, _ = strconv.Atoi(value)
		case "failure_threshold":
			base.FailureThreshold, _ = strconv.Atoi(value)
		case "recovery_threshold":
			base.RecoveryThreshold, _ = strconv.Atoi(value)
		case "max_concurrency":
			base.MaxConcurrency, _ = strconv.Atoi(value)
		case "notify_initial":
			base.NotifyInitial, _ = strconv.ParseBool(value)
		}
	}
	return base
}

func smtpAddress(c SMTPConfig) string { return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) }
