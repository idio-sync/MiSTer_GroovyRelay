package uiserver

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// TestDiffBridgeConfig_SSHFields confirms ssh_user and ssh_password
// edits surface as changed keys so scopeForBridgeField gets a chance
// to dispatch them.
func TestDiffBridgeConfig_SSHFields(t *testing.T) {
	old := config.BridgeConfig{
		MiSTer: config.MisterConfig{
			Host: "192.168.1.42", Port: 32100, SourcePort: 32101,
			SSHUser: "root", SSHPassword: "",
		},
	}
	newCfg := old
	newCfg.MiSTer.SSHUser = "alice"
	newCfg.MiSTer.SSHPassword = "hunter2"

	keys := diffBridgeConfig(old, newCfg)
	if !containsStr(keys, "mister.ssh_user") {
		t.Errorf("expected mister.ssh_user in diff keys, got %v", keys)
	}
	if !containsStr(keys, "mister.ssh_password") {
		t.Errorf("expected mister.ssh_password in diff keys, got %v", keys)
	}
}

// TestScopeForBridgeField_SSHFieldsHotSwap confirms the new SSH keys
// are explicitly hot-swap, not the default ScopeRestartBridge.
func TestScopeForBridgeField_SSHFieldsHotSwap(t *testing.T) {
	for _, k := range []string{"mister.ssh_user", "mister.ssh_password"} {
		t.Run(k, func(t *testing.T) {
			got := scopeForBridgeField(k)
			if got != adapters.ScopeHotSwap {
				t.Errorf("scopeForBridgeField(%q) = %v, want ScopeHotSwap", k, got)
			}
		})
	}
}

// TestDiffBridgeConfig_LoggingDebug pins the diff for the new logging
// toggle so a flip surfaces as a changed key and reaches the apply
// switch.
func TestDiffBridgeConfig_LoggingDebug(t *testing.T) {
	old := config.BridgeConfig{
		Logging: config.LoggingConfig{Debug: false},
	}
	newCfg := old
	newCfg.Logging.Debug = true

	keys := diffBridgeConfig(old, newCfg)
	if !containsStr(keys, "logging.debug") {
		t.Errorf("expected logging.debug in diff keys, got %v", keys)
	}
}

// TestScopeForBridgeField_LoggingDebugHotSwap pins the scope for the
// logging toggle: flipping it must NOT trigger a cast restart or
// container restart — the operator wants to enable diagnostic logs
// against an in-flight session, that's the entire point.
func TestScopeForBridgeField_LoggingDebugHotSwap(t *testing.T) {
	got := scopeForBridgeField("logging.debug")
	if got != adapters.ScopeHotSwap {
		t.Errorf("scopeForBridgeField(logging.debug) = %v, want ScopeHotSwap", got)
	}
}
