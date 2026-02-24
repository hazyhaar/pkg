package mcprt

import (
	"context"
	"strings"
	"testing"
)

func TestGroupIsolation_NoSessionGroup_AllAllowed(t *testing.T) {
	gc := &groupConfig{
		compatible: map[string]map[string]bool{
			"sensitive": {"sensitive": true, "default": true},
			"public":    {"public": true, "default": true},
			"default":   {"sensitive": true, "public": true, "default": true},
		},
	}

	// No group in context — first call, should be allowed.
	err := gc.check(context.Background(), "vault_read", "sensitive")
	if err != nil {
		t.Fatalf("no session group should allow all: %v", err)
	}
}

func TestGroupIsolation_SameGroup_Allowed(t *testing.T) {
	gc := &groupConfig{
		compatible: map[string]map[string]bool{
			"sensitive": {"sensitive": true, "default": true},
			"public":    {"public": true, "default": true},
		},
	}

	ctx := WithGroupSession(context.Background(), "sensitive")
	err := gc.check(ctx, "vault_write", "sensitive")
	if err != nil {
		t.Fatalf("same group should be allowed: %v", err)
	}
}

func TestGroupIsolation_CrossGroup_Blocked(t *testing.T) {
	gc := &groupConfig{
		compatible: map[string]map[string]bool{
			"sensitive": {"sensitive": true, "default": true},
			"public":    {"public": true, "default": true},
		},
	}

	ctx := WithGroupSession(context.Background(), "sensitive")
	err := gc.check(ctx, "search_docs", "public")
	if err == nil {
		t.Fatal("cross-group call should be blocked")
	}
	if !strings.Contains(err.Error(), "isolation") {
		t.Fatalf("error should mention isolation: %v", err)
	}
}

func TestGroupIsolation_DefaultGroup_AlwaysCompatible(t *testing.T) {
	gc := &groupConfig{
		compatible: map[string]map[string]bool{
			"sensitive": {"sensitive": true, "default": true},
			"public":    {"public": true, "default": true},
			"default":   {"sensitive": true, "public": true, "default": true},
		},
	}

	// From sensitive, calling a default-group tool should work.
	ctx := WithGroupSession(context.Background(), "sensitive")
	err := gc.check(ctx, "generic_tool", "default")
	if err != nil {
		t.Fatalf("default group should be compatible with everything: %v", err)
	}

	// From default, calling a sensitive tool should work.
	ctx = WithGroupSession(context.Background(), "default")
	err = gc.check(ctx, "vault_read", "sensitive")
	if err != nil {
		t.Fatalf("default session should access sensitive: %v", err)
	}
}

func TestGroupIsolation_UnknownSessionGroup_Allowed(t *testing.T) {
	gc := &groupConfig{
		compatible: map[string]map[string]bool{
			"sensitive": {"sensitive": true, "default": true},
		},
	}

	ctx := WithGroupSession(context.Background(), "unknown_group")
	err := gc.check(ctx, "vault_read", "sensitive")
	if err != nil {
		t.Fatalf("unknown session group should be allowed (backwards compat): %v", err)
	}
}

func TestGroupIsolation_EmptyToolGroupTag(t *testing.T) {
	gc := &groupConfig{
		compatible: map[string]map[string]bool{
			"sensitive": {"sensitive": true, "default": true},
		},
	}

	// Tool with empty group_tag should default to "default".
	ctx := WithGroupSession(context.Background(), "sensitive")
	err := gc.check(ctx, "some_tool", "")
	if err != nil {
		t.Fatalf("empty group_tag should default to 'default': %v", err)
	}
}

func TestWithGroupIsolation_BridgeOption(t *testing.T) {
	opt := WithGroupIsolation(map[string][]string{
		"sensitive": {"vault_read", "secrets_list"},
		"public":    {"search_docs", "list_items"},
	})

	var cfg bridgeConfig
	opt(&cfg)

	if cfg.groups == nil {
		t.Fatal("groups config should be set")
	}

	// Sensitive → public should be blocked.
	ctx := WithGroupSession(context.Background(), "sensitive")
	err := cfg.groups.check(ctx, "search_docs", "public")
	if err == nil {
		t.Fatal("sensitive → public should be blocked")
	}

	// Sensitive → sensitive should be allowed.
	err = cfg.groups.check(ctx, "vault_read", "sensitive")
	if err != nil {
		t.Fatalf("sensitive → sensitive should be allowed: %v", err)
	}

	// Sensitive → default should be allowed.
	err = cfg.groups.check(ctx, "generic", "default")
	if err != nil {
		t.Fatalf("sensitive → default should be allowed: %v", err)
	}
}

func TestGetGroupSession_Empty(t *testing.T) {
	if got := GetGroupSession(context.Background()); got != "" {
		t.Fatalf("GetGroupSession should return empty: %q", got)
	}
}

func TestWithGroupSession_RoundTrip(t *testing.T) {
	ctx := WithGroupSession(context.Background(), "mygroup")
	if got := GetGroupSession(ctx); got != "mygroup" {
		t.Fatalf("GetGroupSession = %q, want %q", got, "mygroup")
	}
}
