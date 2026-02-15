package controlplane

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hazyhaar/pkg/sqlitedb"
)

func setupTestCP(t *testing.T) *ControlPlane {
	t.Helper()
	db, err := sqlitedb.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	cp := New(db)
	ctx := context.Background()
	if err := cp.Init(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cp.Close() })
	return cp
}

func TestInit(t *testing.T) {
	cp := setupTestCP(t)
	// Verify all 12 tables + metadata exist.
	tables := []string{
		"hpx_config", "hpx_routes", "hpx_middleware", "hpx_services",
		"hpx_ratelimits", "hpx_ratelimit_counters", "hpx_breakers",
		"hpx_certs", "hpx_topics", "hpx_messages", "hpx_subscriptions",
		"hpx_mcp_tools", "hpx_metadata",
	}
	for _, table := range tables {
		var count int
		err := cp.DB().QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count)
		if err != nil || count != 1 {
			t.Fatalf("table %s not found", table)
		}
	}
}

func TestConfig_CRUD(t *testing.T) {
	cp := setupTestCP(t)
	ctx := context.Background()

	// Set.
	if err := cp.SetConfig(ctx, "listen_addr", ":8080", "HTTP listen address"); err != nil {
		t.Fatal(err)
	}

	// Get.
	val, err := cp.GetConfig(ctx, "listen_addr")
	if err != nil {
		t.Fatal(err)
	}
	if val != ":8080" {
		t.Fatalf("got %q", val)
	}

	// Get missing.
	val, err = cp.GetConfig(ctx, "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if val != "" {
		t.Fatalf("expected empty, got %q", val)
	}

	// Update.
	if err := cp.SetConfig(ctx, "listen_addr", ":9090", "updated"); err != nil {
		t.Fatal(err)
	}
	val, _ = cp.GetConfig(ctx, "listen_addr")
	if val != ":9090" {
		t.Fatalf("after update: got %q", val)
	}

	// List.
	count := 0
	for _, err := range cp.ListConfig(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		count++
	}
	if count != 1 {
		t.Fatalf("list count: got %d, want 1", count)
	}

	// Delete.
	if err := cp.DeleteConfig(ctx, "listen_addr"); err != nil {
		t.Fatal(err)
	}
	val, _ = cp.GetConfig(ctx, "listen_addr")
	if val != "" {
		t.Fatalf("after delete: got %q", val)
	}
}

func TestRoutes_CRUD(t *testing.T) {
	cp := setupTestCP(t)
	ctx := context.Background()

	r := Route{
		ServiceName: "billing",
		Strategy:    "local",
		Enabled:     true,
	}
	if err := cp.UpsertRoute(ctx, r); err != nil {
		t.Fatal(err)
	}

	got, err := cp.GetRoute(ctx, "billing")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("route not found")
	}
	if got.Strategy != "local" {
		t.Fatalf("strategy: got %q", got.Strategy)
	}

	// Switch to noop.
	cp.SetRouteStrategy(ctx, "billing", "noop")
	got, _ = cp.GetRoute(ctx, "billing")
	if got.Strategy != "noop" {
		t.Fatalf("after strategy change: got %q", got.Strategy)
	}

	// List enabled routes.
	count := 0
	for _, err := range cp.ListRoutes(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		count++
	}
	if count != 1 {
		t.Fatalf("list count: got %d", count)
	}

	// Delete.
	cp.DeleteRoute(ctx, "billing")
	got, _ = cp.GetRoute(ctx, "billing")
	if got != nil {
		t.Fatal("route should be deleted")
	}
}

func TestServices_CRUD(t *testing.T) {
	cp := setupTestCP(t)
	ctx := context.Background()

	s := ServiceEntry{
		ServiceName: "auth",
		Version:     "1.0.0",
		Host:        "10.0.0.1",
		Port:        8443,
		Protocol:    "http",
		Status:      "active",
	}
	if err := cp.RegisterService(ctx, s); err != nil {
		t.Fatal(err)
	}

	got, err := cp.GetService(ctx, "auth")
	if err != nil || got == nil {
		t.Fatal("service not found")
	}
	if got.Host != "10.0.0.1" {
		t.Fatalf("host: got %q", got.Host)
	}

	// Heartbeat.
	if err := cp.Heartbeat(ctx, "auth"); err != nil {
		t.Fatal(err)
	}

	// List.
	count := 0
	for _, err := range cp.ListServices(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		count++
	}
	if count != 1 {
		t.Fatalf("list count: got %d", count)
	}

	// Deregister.
	cp.DeregisterService(ctx, "auth")
	got, _ = cp.GetService(ctx, "auth")
	if got != nil {
		t.Fatal("should be gone")
	}
}

func TestBreaker_CRUD(t *testing.T) {
	cp := setupTestCP(t)
	ctx := context.Background()

	b := BreakerEntry{
		ServiceName:    "payments",
		State:          "closed",
		Threshold:      5,
		ResetTimeoutMs: 30000,
	}
	if err := cp.UpsertBreaker(ctx, b); err != nil {
		t.Fatal(err)
	}

	got, err := cp.GetBreaker(ctx, "payments")
	if err != nil || got == nil {
		t.Fatal("breaker not found")
	}
	if got.State != "closed" {
		t.Fatalf("state: got %q", got.State)
	}

	// Trip open.
	b.State = "open"
	b.FailureCount = 5
	cp.UpsertBreaker(ctx, b)
	got, _ = cp.GetBreaker(ctx, "payments")
	if got.State != "open" {
		t.Fatalf("after trip: got %q", got.State)
	}

	// Reset.
	cp.ResetBreaker(ctx, "payments")
	got, _ = cp.GetBreaker(ctx, "payments")
	if got.State != "closed" {
		t.Fatalf("after reset: got %q", got.State)
	}
}

func TestMiddleware_Chain(t *testing.T) {
	cp := setupTestCP(t)
	ctx := context.Background()

	// Need a route first (foreign key).
	cp.UpsertRoute(ctx, Route{ServiceName: "api", Strategy: "http", Endpoint: "http://x", Enabled: true})

	cp.AddMiddleware(ctx, "api", "auth", 1, nil)
	cp.AddMiddleware(ctx, "api", "ratelimit", 2, json.RawMessage(`{"max": 100}`))
	cp.AddMiddleware(ctx, "api", "logging", 0, nil)

	chain, err := cp.GetMiddlewareChain(ctx, "api")
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 3 {
		t.Fatalf("chain length: got %d", len(chain))
	}
	// Should be ordered by position: logging(0), auth(1), ratelimit(2).
	if chain[0].Middleware != "logging" {
		t.Fatalf("first middleware: got %q", chain[0].Middleware)
	}
	if chain[2].Middleware != "ratelimit" {
		t.Fatalf("last middleware: got %q", chain[2].Middleware)
	}

	// Remove.
	cp.RemoveMiddleware(ctx, "api", "auth")
	chain, _ = cp.GetMiddlewareChain(ctx, "api")
	if len(chain) != 2 {
		t.Fatalf("after remove: got %d", len(chain))
	}
}

func TestRateLimit_CRUD(t *testing.T) {
	cp := setupTestCP(t)
	ctx := context.Background()

	rule := RateLimitRule{
		RuleID:      "rl_global",
		Scope:       "global",
		MaxRequests: 1000,
		WindowMs:    60000,
		Enabled:     true,
	}
	if err := cp.UpsertRateLimit(ctx, rule); err != nil {
		t.Fatal(err)
	}

	rules, err := cp.GetRateLimitsForService(ctx, "any_service")
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 {
		t.Fatalf("rules count: got %d", len(rules))
	}
	if rules[0].MaxRequests != 1000 {
		t.Fatalf("max_requests: got %d", rules[0].MaxRequests)
	}

	cp.DeleteRateLimit(ctx, "rl_global")
	rules, _ = cp.GetRateLimitsForService(ctx, "any")
	if len(rules) != 0 {
		t.Fatal("should be empty after delete")
	}
}

func TestMCPTools_CRUD(t *testing.T) {
	cp := setupTestCP(t)
	ctx := context.Background()

	tool := MCPTool{
		ToolName:      "sql_query",
		ToolCategory:  "database",
		Description:   "Run SQL query",
		InputSchema:   json.RawMessage(`{"type": "object"}`),
		HandlerType:   "sql_query",
		HandlerConfig: json.RawMessage(`{"db": "main"}`),
		IsActive:      true,
		Version:       1,
	}
	if err := cp.RegisterMCPTool(ctx, tool); err != nil {
		t.Fatal(err)
	}

	tools, err := cp.LoadActiveMCPTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools count: got %d", len(tools))
	}
	if tools["sql_query"] == nil {
		t.Fatal("tool not found")
	}

	// Deactivate.
	cp.DeactivateMCPTool(ctx, "sql_query")
	tools, _ = cp.LoadActiveMCPTools(ctx)
	if len(tools) != 0 {
		t.Fatal("should be empty after deactivate")
	}
}

func TestCerts_CRUD(t *testing.T) {
	cp := setupTestCP(t)
	ctx := context.Background()

	cert := CertEntry{
		Domain:    "horos.local",
		CertPEM:   "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----",
		KeyPEM:    "-----BEGIN PRIVATE KEY-----\ntest\n-----END PRIVATE KEY-----",
		IsDefault: true,
	}
	if err := cp.StoreCert(ctx, cert); err != nil {
		t.Fatal(err)
	}

	got, err := cp.GetCertForDomain(ctx, "horos.local")
	if err != nil || got == nil {
		t.Fatal("cert not found")
	}
	if got.Domain != "horos.local" {
		t.Fatalf("domain: got %q", got.Domain)
	}

	def, err := cp.GetDefaultCert(ctx)
	if err != nil || def == nil {
		t.Fatal("default cert not found")
	}

	cp.DeleteCert(ctx, got.CertID)
	got, _ = cp.GetCertForDomain(ctx, "horos.local")
	if got != nil {
		t.Fatal("should be gone")
	}
}
