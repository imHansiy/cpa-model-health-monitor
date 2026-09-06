package main

import "testing"

func validTarget() Target {
	return Target{ID: "one", Name: "AnyRouter Claude", Enabled: true, Source: "direct", Protocol: "anthropic_messages", BaseURL: "https://api.example.com", Model: "claude-test", APIKey: "secret", AuthMode: "x-api-key"}
}

func TestNormalizeConfigAndSecretRedaction(t *testing.T) {
	cfg := defaultConfig()
	cfg.Targets = []Target{validTarget()}
	cfg.SMTP = SMTPConfig{Enabled: true, Host: "smtp.example.com", Port: 465, TLSMode: "implicit", Username: "user", Password: "mail-secret", From: "a@example.com", To: []string{"b@example.com"}}
	got, err := normalizeConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	public := publicConfig(got)
	if public.SMTP.Password != "" || public.Targets[0].APIKey != "" {
		t.Fatal("public config exposed a secret")
	}
	if !public.SMTPPasswordSet || !public.TargetSecretSet["one"] {
		t.Fatal("secret presence flags are missing")
	}
}

func TestMergeSecretsPreservesBlankReplacement(t *testing.T) {
	old := defaultConfig()
	old.SMTP.Password = "old-mail"
	old.Targets = []Target{validTarget()}
	next := defaultConfig()
	next.SMTP = old.SMTP
	next.SMTP.Password = ""
	next.Targets = []Target{validTarget()}
	next.Targets[0].APIKey = ""
	merged := mergeSecrets(next, old)
	if merged.SMTP.Password != "old-mail" || merged.Targets[0].APIKey != "secret" {
		t.Fatal("blank secret did not preserve existing value")
	}
}

func TestNormalizeRejectsDuplicateTargets(t *testing.T) {
	cfg := defaultConfig()
	cfg.Targets = []Target{validTarget(), validTarget()}
	if _, err := normalizeConfig(cfg); err == nil {
		t.Fatal("expected duplicate target error")
	}
}

func TestNormalizeProviderAndCredentialChecks(t *testing.T) {
	cfg := defaultConfig()
	cfg.Targets = []Target{
		{ID: "provider", Enabled: true, CheckType: "provider", BaseURL: "https://api.example.com/v1"},
		{ID: "credential", Enabled: true, CheckType: "credential", Source: "cpa_auth", AuthIndex: "auth-1"},
	}
	got, err := normalizeConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Targets[0].AuthMode != "none" || got.Targets[0].Source != "direct" {
		t.Fatalf("provider target was not normalized: %+v", got.Targets[0])
	}
}
