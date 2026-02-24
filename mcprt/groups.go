package mcprt

import (
	"context"
	"fmt"
	"sync"
)

// contextKey for group session tracking.
type contextKey string

const groupSessionKey contextKey = "mcprt_group_session"

// WithGroupSession returns a context tagged with the group from the first tool
// called in this session. Subsequent calls in the same context are checked
// against this initial group for isolation.
func WithGroupSession(ctx context.Context, group string) context.Context {
	return context.WithValue(ctx, groupSessionKey, group)
}

// GetGroupSession returns the group session tag from context, or empty string.
func GetGroupSession(ctx context.Context) string {
	v, _ := ctx.Value(groupSessionKey).(string)
	return v
}

// groupConfig holds the isolation rules.
type groupConfig struct {
	// compatible maps a group to the set of groups it can interact with.
	// If a group is not in this map, it's compatible with everything.
	compatible map[string]map[string]bool
	mu         sync.RWMutex
}

// WithGroupIsolation configures group isolation on the Bridge.
// The groups map defines which tools belong to which group.
// Tools in incompatible groups cannot be called in the same session.
//
// Example:
//
//	mcprt.WithGroupIsolation(map[string][]string{
//	    "sensitive": {"vault_read", "secrets_list"},
//	    "public":    {"search_docs", "list_items"},
//	})
//
// In this configuration, if the first tool called is in "sensitive",
// subsequent calls to "public" tools are rejected (and vice versa).
// Tools in the same group or in "default" are always compatible.
func WithGroupIsolation(groups map[string][]string) BridgeOption {
	gc := &groupConfig{
		compatible: make(map[string]map[string]bool),
	}

	// Build the tool→group mapping, and set up compatibility:
	// each group is compatible with itself and with "default".
	for group := range groups {
		gc.compatible[group] = map[string]bool{
			group:     true,
			"default": true,
		}
	}
	// "default" is compatible with everything.
	allGroups := make(map[string]bool)
	for group := range groups {
		allGroups[group] = true
	}
	allGroups["default"] = true
	gc.compatible["default"] = allGroups

	return func(c *bridgeConfig) { c.groups = gc }
}

// check verifies that calling a tool with the given groupTag is allowed
// in the current session context.
func (gc *groupConfig) check(ctx context.Context, toolName, toolGroupTag string) error {
	sessionGroup := GetGroupSession(ctx)
	if sessionGroup == "" {
		// No session group set yet — first call, no restriction.
		return nil
	}

	if toolGroupTag == "" {
		toolGroupTag = "default"
	}

	// Same group is always allowed.
	if sessionGroup == toolGroupTag {
		return nil
	}

	gc.mu.RLock()
	compat, exists := gc.compatible[sessionGroup]
	gc.mu.RUnlock()

	if !exists {
		// Unknown session group — allow (backwards compatible).
		return nil
	}

	if compat[toolGroupTag] {
		return nil
	}

	return fmt.Errorf("tool %q (group %q) cannot be called from session group %q: group isolation violation",
		toolName, toolGroupTag, sessionGroup)
}
