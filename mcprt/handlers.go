package mcprt

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hazyhaar/pkg/idgen"
)

// maxQueryRows limits the number of rows returned by SQLQueryHandler to
// prevent memory exhaustion from unbounded result sets.
const maxQueryRows = 10_000

// isReadOnlySQL checks whether a SQL statement appears to be read-only.
// Used to enforce Mode="readonly" on sql_query tools.
func isReadOnlySQL(query string) bool {
	q := strings.TrimSpace(strings.ToUpper(query))
	return strings.HasPrefix(q, "SELECT") ||
		strings.HasPrefix(q, "WITH") ||
		strings.HasPrefix(q, "EXPLAIN") ||
		strings.HasPrefix(q, "PRAGMA")
}

// SQLQueryHandler executes a SELECT and returns results as JSON.
type SQLQueryHandler struct{ DB *sql.DB }

func (h *SQLQueryHandler) Execute(ctx context.Context, tool *DynamicTool, params map[string]any) (string, error) {
	query, ok := tool.HandlerConfig["query"].(string)
	if !ok {
		return "", fmt.Errorf("handler_config missing 'query'")
	}
	if tool.Mode == ModeReadonly && !isReadOnlySQL(query) {
		return "", fmt.Errorf("tool %q is readonly: write queries not allowed", tool.Name)
	}
	paramsConfig, _ := tool.HandlerConfig["params"].([]any)
	resultFormat, _ := tool.HandlerConfig["result_format"].(string)
	if resultFormat == "" {
		resultFormat = "array"
	}

	args := resolveParams(paramsConfig, params)
	rows, err := h.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return "", fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return "", err
	}

	var results []map[string]any
	for rows.Next() {
		if len(results) >= maxQueryRows {
			return "", fmt.Errorf("query exceeded max result rows (%d)", maxQueryRows)
		}
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return "", err
		}
		row := make(map[string]any, len(columns))
		for i, col := range columns {
			if b, ok := values[i].([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = values[i]
			}
		}
		results = append(results, row)
	}

	var output any
	if resultFormat == "object" && len(results) > 0 {
		output = results[0]
	} else {
		if results == nil {
			results = []map[string]any{}
		}
		output = results
	}

	data, err := json.Marshal(output)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// SQLScriptHandler executes multi-statement transactions.
type SQLScriptHandler struct {
	DB    *sql.DB
	NewID idgen.Generator
}

func (h *SQLScriptHandler) Execute(ctx context.Context, tool *DynamicTool, params map[string]any) (string, error) {
	stmtsConfig, ok := tool.HandlerConfig["statements"].([]any)
	if !ok {
		return "", fmt.Errorf("handler_config missing 'statements'")
	}

	useTx, _ := tool.HandlerConfig["transaction"].(bool)
	returnDirective, _ := tool.HandlerConfig["return"].(string)

	var tx *sql.Tx
	var err error
	if useTx {
		tx, err = h.DB.BeginTx(ctx, nil)
		if err != nil {
			return "", fmt.Errorf("begin tx: %w", err)
		}
		defer tx.Rollback()
	}

	var lastInsertID, totalAffected int64

	for i, sc := range stmtsConfig {
		stmtMap, ok := sc.(map[string]any)
		if !ok {
			return "", fmt.Errorf("statement %d: not a map", i)
		}
		sqlStmt, ok := stmtMap["sql"].(string)
		if !ok {
			return "", fmt.Errorf("statement %d: missing 'sql'", i)
		}
		stmtParams, _ := stmtMap["params"].([]any)
		args := h.resolveTemplateParams(stmtParams, params)

		var result sql.Result
		if useTx {
			result, err = tx.ExecContext(ctx, sqlStmt, args...)
		} else {
			result, err = h.DB.ExecContext(ctx, sqlStmt, args...)
		}
		if err != nil {
			return "", fmt.Errorf("statement %d failed: %w", i, err)
		}
		if id, e := result.LastInsertId(); e == nil {
			lastInsertID = id
		}
		if n, e := result.RowsAffected(); e == nil {
			totalAffected += n
		}
	}

	if useTx {
		if err := tx.Commit(); err != nil {
			return "", fmt.Errorf("commit: %w", err)
		}
	}

	var output map[string]any
	switch returnDirective {
	case "last_insert_rowid":
		output = map[string]any{"last_insert_id": lastInsertID}
	case "affected_rows":
		output = map[string]any{"affected_rows": totalAffected}
	default:
		output = map[string]any{"success": true, "affected_rows": totalAffected, "last_insert_id": lastInsertID}
	}

	data, err := json.Marshal(output)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func resolveParams(paramsConfig []any, params map[string]any) []any {
	var args []any
	for _, p := range paramsConfig {
		name, ok := p.(string)
		if !ok {
			args = append(args, nil)
			continue
		}
		if val, exists := params[name]; exists {
			args = append(args, val)
		} else {
			args = append(args, nil)
		}
	}
	return args
}

func (h *SQLScriptHandler) resolveTemplateParams(paramsConfig []any, params map[string]any) []any {
	var args []any
	for _, p := range paramsConfig {
		tmpl, ok := p.(string)
		if !ok {
			args = append(args, nil)
			continue
		}
		if strings.HasPrefix(tmpl, "{{") && strings.HasSuffix(tmpl, "}}") {
			expr := strings.TrimSpace(tmpl[2 : len(tmpl)-2])
			switch expr {
			case "uuid()":
				args = append(args, h.NewID())
			case "now()":
				args = append(args, time.Now().Unix())
			default:
				// Reject unknown function-like expressions to prevent
				// injection of arbitrary template functions.
				if strings.Contains(expr, "(") {
					args = append(args, nil)
					continue
				}
				if val, exists := params[expr]; exists {
					args = append(args, val)
				} else {
					args = append(args, nil)
				}
			}
		} else if val, exists := params[tmpl]; exists {
			args = append(args, val)
		} else {
			args = append(args, nil)
		}
	}
	return args
}
