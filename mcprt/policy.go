package mcprt

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/hazyhaar/pkg/kit"
)

// DBPolicy evaluates per-tool access rules stored in mcp_tool_policy.
//
// Evaluation logic:
//   - If any DENY rule matches the caller's role → deny.
//   - If ALLOW rules exist for the tool but none match → deny.
//   - If no rules exist for the tool → allow (default open, backwards compatible).
type DBPolicy struct {
	db *sql.DB
}

// NewDBPolicy creates a PolicyFunc backed by the mcp_tool_policy table.
func NewDBPolicy(db *sql.DB) PolicyFunc {
	p := &DBPolicy{db: db}
	return p.Evaluate
}

// Evaluate checks whether the current caller (identified by role in context)
// is allowed to execute the named tool.
func (p *DBPolicy) Evaluate(ctx context.Context, toolName string) error {
	role := kit.GetRole(ctx)
	if role == "" {
		role = "*"
	}

	rows, err := p.db.QueryContext(ctx,
		`SELECT effect, role FROM mcp_tool_policy WHERE tool_name = ? ORDER BY effect`, toolName)
	if err != nil {
		return fmt.Errorf("policy query: %w", err)
	}
	defer rows.Close()

	var hasAllow, matchesAllow bool

	for rows.Next() {
		var effect, ruleRole string
		if err := rows.Scan(&effect, &ruleRole); err != nil {
			return err
		}

		matches := ruleRole == "*" || ruleRole == role

		if effect == "deny" && matches {
			return fmt.Errorf("tool %q denied for role %q", toolName, role)
		}
		if effect == "allow" {
			hasAllow = true
			if matches {
				matchesAllow = true
			}
		}
	}

	// If allow rules exist but none match this role, deny.
	if hasAllow && !matchesAllow {
		return fmt.Errorf("tool %q not allowed for role %q", toolName, role)
	}

	return nil
}
