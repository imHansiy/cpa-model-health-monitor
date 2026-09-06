package main

import (
	"testing"
	"time"
)

func TestStateChangesOnlyAfterThresholdAndBaselinesSilently(t *testing.T) {
	cfg := defaultConfig()
	cfg.FailureThreshold = 2
	cfg.RecoveryThreshold = 2
	cfg.SMTP.Enabled = false
	r := NewRuntime(&fakeHost{}, t.TempDir())
	r.config = cfg
	at := time.Now().UTC()
	r.applyResult(cfg, ProbeResult{TargetID: "x", Name: "X", Healthy: true, Status: "healthy", CheckedAt: at})
	if r.state.Targets["x"].Status != "up" || r.state.Targets["x"].NotifiedStatus != "up" {
		t.Fatalf("bad baseline: %+v", r.state.Targets["x"])
	}
	r.applyResult(cfg, ProbeResult{TargetID: "x", Name: "X", Healthy: false, Status: "request_error", CheckedAt: at.Add(time.Minute)})
	if r.state.Targets["x"].Status != "up" {
		t.Fatal("changed before failure threshold")
	}
	r.applyResult(cfg, ProbeResult{TargetID: "x", Name: "X", Healthy: false, Status: "request_error", CheckedAt: at.Add(2 * time.Minute)})
	if r.state.Targets["x"].Status != "down" {
		t.Fatal("did not change at failure threshold")
	}
	r.applyResult(cfg, ProbeResult{TargetID: "x", Name: "X", Healthy: true, Status: "healthy", CheckedAt: at.Add(3 * time.Minute)})
	if r.state.Targets["x"].Status != "down" {
		t.Fatal("recovered before threshold")
	}
	r.applyResult(cfg, ProbeResult{TargetID: "x", Name: "X", Healthy: true, Status: "healthy", CheckedAt: at.Add(4 * time.Minute)})
	if r.state.Targets["x"].Status != "up" {
		t.Fatal("did not recover at threshold")
	}
}

func TestStatePersistsAcrossRuntime(t *testing.T) {
	dir := t.TempDir()
	r := NewRuntime(&fakeHost{}, dir)
	cfg := defaultConfig()
	r.applyResult(cfg, ProbeResult{TargetID: "x", Name: "X", Healthy: false, CheckedAt: time.Now().UTC()})
	r.persistState()
	loaded := NewRuntime(&fakeHost{}, dir)
	if loaded.state.Targets["x"] == nil || loaded.state.Targets["x"].Status != "down" {
		t.Fatal("state was not restored")
	}
}
