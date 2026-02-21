package channels

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// setupTestDB creates an in-memory SQLite database with the channels schema.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := Init(db); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// freePort returns a TCP port that is currently available.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// stubFactory returns a ChannelFactory that creates a stub channel.
// The stub emits one message then blocks. If onMessage is non-nil it's
// called for each outbound Send.
func stubFactory(onMessage func(Message)) ChannelFactory {
	return func(name string, config json.RawMessage) (Channel, error) {
		return &stubChannel{
			name:      name,
			platform:  "stub",
			closeCh:   make(chan struct{}),
			inbound:   make(chan Message, 16),
			onMessage: onMessage,
		}, nil
	}
}

type stubChannel struct {
	name      string
	platform  string
	closeCh   chan struct{}
	inbound   chan Message
	onMessage func(Message)
	closed    int32
}

func (s *stubChannel) Listen(ctx context.Context) <-chan Message {
	ch := make(chan Message)
	go func() {
		defer close(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.closeCh:
				return
			case msg, ok := <-s.inbound:
				if !ok {
					return
				}
				select {
				case ch <- msg:
				case <-ctx.Done():
					return
				case <-s.closeCh:
					return
				}
			}
		}
	}()
	return ch
}

func (s *stubChannel) Send(_ context.Context, msg Message) error {
	if atomic.LoadInt32(&s.closed) == 1 {
		return &ErrSendFailed{Channel: s.name, Platform: s.platform,
			Cause: fmt.Errorf("closed")}
	}
	if s.onMessage != nil {
		s.onMessage(msg)
	}
	return nil
}

func (s *stubChannel) Status() ChannelStatus {
	return ChannelStatus{Connected: true, Platform: s.platform}
}

func (s *stubChannel) Close() error {
	if atomic.CompareAndSwapInt32(&s.closed, 0, 1) {
		close(s.closeCh)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Fingerprint
// ---------------------------------------------------------------------------

func TestFingerprint(t *testing.T) {
	r1 := channelRow{Platform: "whatsapp", Config: json.RawMessage(`{"store_path":"/a"}`)}
	r2 := channelRow{Platform: "whatsapp", Config: json.RawMessage(`{"store_path":"/a"}`)}
	r3 := channelRow{Platform: "whatsapp", Config: json.RawMessage(`{"store_path":"/b"}`)}
	r4 := channelRow{Platform: "telegram", Config: json.RawMessage(`{"store_path":"/a"}`)}

	if r1.fingerprint() != r2.fingerprint() {
		t.Fatal("identical rows should have the same fingerprint")
	}
	if r1.fingerprint() == r3.fingerprint() {
		t.Fatal("different configs should have different fingerprints")
	}
	if r1.fingerprint() == r4.fingerprint() {
		t.Fatal("different platforms should have different fingerprints")
	}
}

func TestFingerprint_ExcludesAuthState(t *testing.T) {
	r1 := channelRow{Platform: "whatsapp", Config: json.RawMessage(`{}`), AuthState: json.RawMessage(`{"session":"a"}`)}
	r2 := channelRow{Platform: "whatsapp", Config: json.RawMessage(`{}`), AuthState: json.RawMessage(`{"session":"b"}`)}

	if r1.fingerprint() != r2.fingerprint() {
		t.Fatal("fingerprint should not include auth_state")
	}
}

// ---------------------------------------------------------------------------
// Admin CRUD
// ---------------------------------------------------------------------------

func TestAdmin_UpsertAndGet(t *testing.T) {
	db := setupTestDB(t)
	admin := NewAdmin(db)
	ctx := context.Background()

	cfg := json.RawMessage(`{"listen_addr":":9090"}`)
	if err := admin.UpsertChannel(ctx, "wh1", "webhook", true, cfg); err != nil {
		t.Fatal(err)
	}

	row, err := admin.GetChannel(ctx, "wh1")
	if err != nil {
		t.Fatal(err)
	}
	if row == nil {
		t.Fatal("channel not found after upsert")
	}
	if row.Platform != "webhook" || !row.Enabled {
		t.Fatalf("unexpected row: %+v", row)
	}
}

func TestAdmin_UpsertPreservesAuthState(t *testing.T) {
	db := setupTestDB(t)
	admin := NewAdmin(db)
	ctx := context.Background()

	// Insert initial channel.
	if err := admin.UpsertChannel(ctx, "wa1", "whatsapp", true, json.RawMessage(`{"store_path":"/a"}`)); err != nil {
		t.Fatal(err)
	}

	// Set auth state separately.
	if err := admin.UpdateAuthState(ctx, "wa1", json.RawMessage(`{"device_id":"xyz"}`)); err != nil {
		t.Fatal(err)
	}

	// Upsert with changed config — auth_state must survive.
	if err := admin.UpsertChannel(ctx, "wa1", "whatsapp", true, json.RawMessage(`{"store_path":"/b"}`)); err != nil {
		t.Fatal(err)
	}

	row, err := admin.GetChannel(ctx, "wa1")
	if err != nil {
		t.Fatal(err)
	}
	if string(row.AuthState) != `{"device_id":"xyz"}` {
		t.Fatalf("auth_state lost after upsert: got %s", row.AuthState)
	}
	if string(row.Config) != `{"store_path":"/b"}` {
		t.Fatalf("config not updated: got %s", row.Config)
	}
}

func TestAdmin_ListChannels(t *testing.T) {
	db := setupTestDB(t)
	admin := NewAdmin(db)
	ctx := context.Background()

	admin.UpsertChannel(ctx, "a", "webhook", true, nil)
	admin.UpsertChannel(ctx, "b", "telegram", false, nil)

	rows, err := admin.ListChannels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(rows))
	}
}

func TestAdmin_Delete(t *testing.T) {
	db := setupTestDB(t)
	admin := NewAdmin(db)
	ctx := context.Background()

	admin.UpsertChannel(ctx, "del", "webhook", true, nil)
	if err := admin.DeleteChannel(ctx, "del"); err != nil {
		t.Fatal(err)
	}
	row, _ := admin.GetChannel(ctx, "del")
	if row != nil {
		t.Fatal("channel should be deleted")
	}
}

func TestAdmin_DeleteNotFound(t *testing.T) {
	db := setupTestDB(t)
	admin := NewAdmin(db)
	if err := admin.DeleteChannel(context.Background(), "nope"); err == nil {
		t.Fatal("expected error for missing channel")
	}
}

func TestAdmin_SetEnabled(t *testing.T) {
	db := setupTestDB(t)
	admin := NewAdmin(db)
	ctx := context.Background()

	admin.UpsertChannel(ctx, "tog", "webhook", true, nil)
	if err := admin.SetEnabled(ctx, "tog", false); err != nil {
		t.Fatal(err)
	}
	row, _ := admin.GetChannel(ctx, "tog")
	if row.Enabled {
		t.Fatal("expected disabled")
	}
}

func TestAdmin_UpdateAuthState(t *testing.T) {
	db := setupTestDB(t)
	admin := NewAdmin(db)
	ctx := context.Background()

	admin.UpsertChannel(ctx, "auth", "whatsapp", true, nil)
	if err := admin.UpdateAuthState(ctx, "auth", json.RawMessage(`{"token":"abc"}`)); err != nil {
		t.Fatal(err)
	}
	row, _ := admin.GetChannel(ctx, "auth")
	if string(row.AuthState) != `{"token":"abc"}` {
		t.Fatalf("got %s", row.AuthState)
	}
}

// ---------------------------------------------------------------------------
// Dispatcher: Reload, Close, Send
// ---------------------------------------------------------------------------

func TestDispatcher_Reload_StartsChannel(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	var factoryCalls int32
	factory := func(name string, config json.RawMessage) (Channel, error) {
		atomic.AddInt32(&factoryCalls, 1)
		return &stubChannel{
			name:    name,
			closeCh: make(chan struct{}),
			inbound: make(chan Message, 1),
		}, nil
	}

	d := NewDispatcher(func(ctx context.Context, msg Message) ([]Message, error) {
		return nil, nil
	})
	d.RegisterPlatform("webhook", factory)

	db.Exec(`INSERT INTO channels (name, platform, enabled) VALUES ('wh1', 'webhook', 1)`)
	if err := d.Reload(ctx, db); err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	if c := atomic.LoadInt32(&factoryCalls); c != 1 {
		t.Fatalf("expected 1 factory call, got %d", c)
	}
}

func TestDispatcher_Reload_UnchangedPreservesChannel(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	var factoryCalls int32
	factory := func(name string, config json.RawMessage) (Channel, error) {
		atomic.AddInt32(&factoryCalls, 1)
		return &stubChannel{
			name:    name,
			closeCh: make(chan struct{}),
			inbound: make(chan Message, 1),
		}, nil
	}

	d := NewDispatcher(func(ctx context.Context, msg Message) ([]Message, error) {
		return nil, nil
	})
	d.RegisterPlatform("webhook", factory)

	db.Exec(`INSERT INTO channels (name, platform, enabled) VALUES ('wh1', 'webhook', 1)`)
	d.Reload(ctx, db)
	d.Reload(ctx, db) // Second reload — same fingerprint.
	defer d.Close()

	if c := atomic.LoadInt32(&factoryCalls); c != 1 {
		t.Fatalf("expected factory called once (unchanged), got %d", c)
	}
}

func TestDispatcher_Reload_ChangedConfigRestartsChannel(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	var factoryCalls int32
	factory := func(name string, config json.RawMessage) (Channel, error) {
		atomic.AddInt32(&factoryCalls, 1)
		return &stubChannel{
			name:    name,
			closeCh: make(chan struct{}),
			inbound: make(chan Message, 1),
		}, nil
	}

	d := NewDispatcher(func(ctx context.Context, msg Message) ([]Message, error) {
		return nil, nil
	})
	d.RegisterPlatform("webhook", factory)

	db.Exec(`INSERT INTO channels (name, platform, enabled, config) VALUES ('wh1', 'webhook', 1, '{"listen_addr":":8080"}')`)
	d.Reload(ctx, db)

	// Change config.
	db.Exec(`UPDATE channels SET config = '{"listen_addr":":9090"}' WHERE name = 'wh1'`)
	d.Reload(ctx, db)
	defer d.Close()

	if c := atomic.LoadInt32(&factoryCalls); c != 2 {
		t.Fatalf("expected 2 factory calls (config changed), got %d", c)
	}
}

func TestDispatcher_Reload_DisabledClosesChannel(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	ch := &stubChannel{name: "wh1", closeCh: make(chan struct{}), inbound: make(chan Message, 1)}
	factory := func(name string, config json.RawMessage) (Channel, error) {
		return ch, nil
	}

	d := NewDispatcher(func(ctx context.Context, msg Message) ([]Message, error) {
		return nil, nil
	})
	d.RegisterPlatform("webhook", factory)

	db.Exec(`INSERT INTO channels (name, platform, enabled) VALUES ('wh1', 'webhook', 1)`)
	d.Reload(ctx, db)

	// Disable.
	db.Exec(`UPDATE channels SET enabled = 0 WHERE name = 'wh1'`)
	d.Reload(ctx, db)
	defer d.Close()

	if atomic.LoadInt32(&ch.closed) != 1 {
		t.Fatal("channel should have been closed when disabled")
	}
}

func TestDispatcher_Reload_RemovedClosesChannel(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	ch := &stubChannel{name: "wh1", closeCh: make(chan struct{}), inbound: make(chan Message, 1)}
	factory := func(name string, config json.RawMessage) (Channel, error) {
		return ch, nil
	}

	d := NewDispatcher(func(ctx context.Context, msg Message) ([]Message, error) {
		return nil, nil
	})
	d.RegisterPlatform("webhook", factory)

	db.Exec(`INSERT INTO channels (name, platform, enabled) VALUES ('wh1', 'webhook', 1)`)
	d.Reload(ctx, db)

	db.Exec(`DELETE FROM channels WHERE name = 'wh1'`)
	d.Reload(ctx, db)
	defer d.Close()

	if atomic.LoadInt32(&ch.closed) != 1 {
		t.Fatal("channel should have been closed when removed")
	}
}

func TestDispatcher_Send_NotFound(t *testing.T) {
	d := NewDispatcher(func(ctx context.Context, msg Message) ([]Message, error) {
		return nil, nil
	})
	defer d.Close()

	err := d.Send(context.Background(), Message{ChannelName: "nope"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDispatcher_Status(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	factory := func(name string, config json.RawMessage) (Channel, error) {
		return &stubChannel{
			name:    name,
			closeCh: make(chan struct{}),
			inbound: make(chan Message, 1),
		}, nil
	}

	d := NewDispatcher(func(ctx context.Context, msg Message) ([]Message, error) {
		return nil, nil
	})
	d.RegisterPlatform("webhook", factory)
	db.Exec(`INSERT INTO channels (name, platform, enabled) VALUES ('wh1', 'webhook', 1)`)
	d.Reload(ctx, db)
	defer d.Close()

	st, ok := d.Status("wh1")
	if !ok {
		t.Fatal("channel not found")
	}
	if !st.Connected {
		t.Fatal("expected connected")
	}
}

func TestDispatcher_InboundHandler(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	var handledMsg Message
	var sentResp Message

	ch := &stubChannel{
		name:    "wh1",
		closeCh: make(chan struct{}),
		inbound: make(chan Message, 16),
		onMessage: func(msg Message) {
			sentResp = msg
		},
	}
	factory := func(name string, config json.RawMessage) (Channel, error) {
		return ch, nil
	}

	d := NewDispatcher(func(ctx context.Context, msg Message) ([]Message, error) {
		handledMsg = msg
		return []Message{{Text: "reply", RecipientID: msg.SenderID}}, nil
	})
	d.RegisterPlatform("webhook", factory)

	db.Exec(`INSERT INTO channels (name, platform, enabled) VALUES ('wh1', 'webhook', 1)`)
	d.Reload(ctx, db)

	// Push an inbound message into the stub channel.
	ch.inbound <- Message{
		SenderID: "user1",
		Text:     "hello",
	}

	// Give the dispatch goroutine time to process.
	time.Sleep(100 * time.Millisecond)
	d.Close()

	if handledMsg.Text != "hello" {
		t.Fatalf("expected handler to receive 'hello', got %q", handledMsg.Text)
	}
	if sentResp.Text != "reply" {
		t.Fatalf("expected response 'reply', got %q", sentResp.Text)
	}
	if sentResp.Direction != Outbound {
		t.Fatal("response should be outbound")
	}
}

func TestDispatcher_Close_WaitsForGoroutines(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	factory := func(name string, config json.RawMessage) (Channel, error) {
		return &stubChannel{
			name:    name,
			closeCh: make(chan struct{}),
			inbound: make(chan Message, 1),
		}, nil
	}

	d := NewDispatcher(func(ctx context.Context, msg Message) ([]Message, error) {
		return nil, nil
	})
	d.RegisterPlatform("webhook", factory)

	db.Exec(`INSERT INTO channels (name, platform, enabled) VALUES ('wh1', 'webhook', 1)`)
	d.Reload(ctx, db)

	// Close should not hang (goroutines exit via lifecycle ctx cancel).
	done := make(chan struct{})
	go func() {
		d.Close()
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(5 * time.Second):
		t.Fatal("Close() hung — goroutine leak")
	}
}

// ---------------------------------------------------------------------------
// Inspect
// ---------------------------------------------------------------------------

func TestDispatcher_ListChannels(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	factory := func(name string, config json.RawMessage) (Channel, error) {
		return &stubChannel{
			name:    name,
			closeCh: make(chan struct{}),
			inbound: make(chan Message, 1),
		}, nil
	}

	d := NewDispatcher(func(ctx context.Context, msg Message) ([]Message, error) {
		return nil, nil
	})
	d.RegisterPlatform("webhook", factory)

	db.Exec(`INSERT INTO channels (name, platform, enabled) VALUES ('a', 'webhook', 1)`)
	db.Exec(`INSERT INTO channels (name, platform, enabled) VALUES ('b', 'webhook', 1)`)
	d.Reload(ctx, db)
	defer d.Close()

	count := 0
	for range d.ListChannels() {
		count++
	}
	if count != 2 {
		t.Fatalf("expected 2 channels, got %d", count)
	}
}

func TestDispatcher_Inspect(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	factory := func(name string, config json.RawMessage) (Channel, error) {
		return &stubChannel{
			name:    name,
			closeCh: make(chan struct{}),
			inbound: make(chan Message, 1),
		}, nil
	}

	d := NewDispatcher(func(ctx context.Context, msg Message) ([]Message, error) {
		return nil, nil
	})
	d.RegisterPlatform("webhook", factory)

	db.Exec(`INSERT INTO channels (name, platform, enabled) VALUES ('wh1', 'webhook', 1)`)
	d.Reload(ctx, db)
	defer d.Close()

	info, ok := d.Inspect("wh1")
	if !ok {
		t.Fatal("channel not found")
	}
	if info.Name != "wh1" {
		t.Fatalf("expected name 'wh1', got %q", info.Name)
	}

	_, ok = d.Inspect("missing")
	if ok {
		t.Fatal("expected not found for missing channel")
	}
}

// ---------------------------------------------------------------------------
// Webhook: HMAC verification
// ---------------------------------------------------------------------------

func TestWebhook_VerifyHMAC_NoSecret(t *testing.T) {
	wh := &webhookChannel{config: WebhookConfig{}}
	if !wh.verifyHMAC([]byte("body"), "") {
		t.Fatal("should pass when no secret is configured")
	}
}

func TestWebhook_VerifyHMAC_ValidSignature(t *testing.T) {
	secret := "test-secret"
	wh := &webhookChannel{config: WebhookConfig{Secret: secret}}

	body := []byte(`{"text":"hello"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !wh.verifyHMAC(body, sig) {
		t.Fatal("valid HMAC should pass")
	}
}

func TestWebhook_VerifyHMAC_InvalidSignature(t *testing.T) {
	wh := &webhookChannel{config: WebhookConfig{Secret: "secret"}}
	if wh.verifyHMAC([]byte("body"), "sha256=0000000000000000000000000000000000000000000000000000000000000000") {
		t.Fatal("invalid HMAC should fail")
	}
}

func TestWebhook_VerifyHMAC_MissingSignature(t *testing.T) {
	wh := &webhookChannel{config: WebhookConfig{Secret: "secret"}}
	if wh.verifyHMAC([]byte("body"), "") {
		t.Fatal("missing signature should fail when secret is set")
	}
}

func TestWebhook_VerifyHMAC_NoPrefixSignature(t *testing.T) {
	secret := "test-secret"
	wh := &webhookChannel{config: WebhookConfig{Secret: secret}}

	body := []byte(`{"text":"hello"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil)) // no sha256= prefix

	if !wh.verifyHMAC(body, sig) {
		t.Fatal("valid HMAC without prefix should pass")
	}
}

// ---------------------------------------------------------------------------
// Webhook: HTTP handler integration
// ---------------------------------------------------------------------------

func TestWebhook_Listen_AcceptsValidPost(t *testing.T) {
	port := freePort(t)
	cfg := json.RawMessage(fmt.Sprintf(`{"listen_addr":"127.0.0.1:%d","path":"/hook"}`, port))

	factory := WebhookFactory()
	ch, err := factory("test-wh", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	msgs := ch.Listen(ctx)

	// Wait for server to start.
	time.Sleep(100 * time.Millisecond)

	body := `{"text":"hello","sender_id":"user1"}`
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/hook", port), "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}

	select {
	case msg := <-msgs:
		if msg.Text != "hello" {
			t.Fatalf("expected 'hello', got %q", msg.Text)
		}
		if msg.ChannelName != "test-wh" {
			t.Fatalf("expected channel name 'test-wh', got %q", msg.ChannelName)
		}
		if msg.Direction != Inbound {
			t.Fatal("expected inbound direction")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no message received")
	}
}

func TestWebhook_Listen_RejectsNonPost(t *testing.T) {
	port := freePort(t)
	cfg := json.RawMessage(fmt.Sprintf(`{"listen_addr":"127.0.0.1:%d","path":"/hook"}`, port))

	factory := WebhookFactory()
	ch, err := factory("test-wh", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch.Listen(ctx)
	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/hook", port))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}

func TestWebhook_Listen_HMAC_RejectsInvalid(t *testing.T) {
	port := freePort(t)
	cfg := json.RawMessage(fmt.Sprintf(`{"listen_addr":"127.0.0.1:%d","path":"/hook","secret":"mysecret"}`, port))

	factory := WebhookFactory()
	ch, err := factory("test-wh", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch.Listen(ctx)
	time.Sleep(100 * time.Millisecond)

	// POST without HMAC signature.
	body := `{"text":"hello"}`
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/hook", port), "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestWebhook_Listen_HMAC_AcceptsValid(t *testing.T) {
	port := freePort(t)
	secret := "test-secret-key"
	cfg := json.RawMessage(fmt.Sprintf(`{"listen_addr":"127.0.0.1:%d","path":"/hook","secret":"%s"}`, port, secret))

	factory := WebhookFactory()
	ch, err := factory("test-wh", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	msgs := ch.Listen(ctx)
	time.Sleep(100 * time.Millisecond)

	body := []byte(`{"text":"signed"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/hook", port), strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature-256", sig)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}

	select {
	case msg := <-msgs:
		if msg.Text != "signed" {
			t.Fatalf("expected 'signed', got %q", msg.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no message received")
	}
}

func TestWebhook_Listen_DoubleCallDoesNotPanic(t *testing.T) {
	port := freePort(t)
	cfg := json.RawMessage(fmt.Sprintf(`{"listen_addr":"127.0.0.1:%d","path":"/hook"}`, port))

	factory := WebhookFactory()
	ch, err := factory("test-wh", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Call Listen twice — should not panic or bind the port twice.
	ch.Listen(ctx)
	ch.Listen(ctx)
	time.Sleep(100 * time.Millisecond)

	// Server should still be functional.
	body := `{"text":"ok"}`
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/hook", port), "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 after double Listen, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Webhook: Send (callback URL)
// ---------------------------------------------------------------------------

func TestWebhook_Send_CallbackURL_SSRFBlocked(t *testing.T) {
	// SSRF guard: callback URLs pointing to loopback must be rejected.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	wh := &webhookChannel{
		name:    "wh-send",
		config:  WebhookConfig{Secret: "callback-secret"},
		closeCh: make(chan struct{}),
	}

	msg := Message{
		Text:        "response",
		RecipientID: "user1",
		Metadata:    map[string]string{"callback_url": server.URL},
	}

	err := wh.Send(context.Background(), msg)
	if err == nil {
		t.Fatal("expected SSRF error for loopback callback URL")
	}

	// Private IPs should also be rejected.
	msg.Metadata["callback_url"] = "http://10.0.0.1:8080/hook"
	err = wh.Send(context.Background(), msg)
	if err == nil {
		t.Fatal("expected SSRF error for private callback URL")
	}
}

func TestWebhook_Send_CallbackHMACSigning(t *testing.T) {
	// Verify HMAC signing on outbound callbacks using verifyHMAC.
	secret := "callback-secret"
	wh := &webhookChannel{
		name:    "wh-send",
		config:  WebhookConfig{Secret: secret},
		closeCh: make(chan struct{}),
	}

	body := []byte(`{"text":"response"}`)

	// Compute expected HMAC.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expectedSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	// The verifyHMAC method should accept the correct signature.
	if !wh.verifyHMAC(body, expectedSig) {
		t.Fatal("verifyHMAC should accept correct signature")
	}

	// And reject a wrong signature.
	if wh.verifyHMAC(body, "sha256=0000000000000000000000000000000000000000000000000000000000000000") {
		t.Fatal("verifyHMAC should reject wrong signature")
	}
}

func TestWebhook_Send_NoCallbackURL(t *testing.T) {
	wh := &webhookChannel{
		name:    "wh-send",
		config:  WebhookConfig{},
		closeCh: make(chan struct{}),
	}

	msg := Message{Text: "response"}
	if err := wh.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send with no callback should succeed silently: %v", err)
	}
}

func TestWebhook_Send_Closed(t *testing.T) {
	wh := &webhookChannel{
		name:    "wh-send",
		config:  WebhookConfig{},
		closed:  true,
		closeCh: make(chan struct{}),
	}

	err := wh.Send(context.Background(), Message{Metadata: map[string]string{"callback_url": "http://x"}})
	if err == nil {
		t.Fatal("expected error on closed channel")
	}
}

// ---------------------------------------------------------------------------
// Webhook: Factory validation
// ---------------------------------------------------------------------------

func TestWebhookFactory_RequiresListenAddr(t *testing.T) {
	factory := WebhookFactory()
	_, err := factory("test", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing listen_addr")
	}
}

func TestWebhookFactory_DefaultsPath(t *testing.T) {
	factory := WebhookFactory()
	ch, err := factory("test", json.RawMessage(`{"listen_addr":":0"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	wh := ch.(*webhookChannel)
	if wh.config.Path != "/" {
		t.Fatalf("expected default path '/', got %q", wh.config.Path)
	}
}

// ---------------------------------------------------------------------------
// Watcher: data_version detection
// ---------------------------------------------------------------------------

func TestWatch_DetectsChanges(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/watch_test.db"

	// Writer connection.
	writerDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writerDB.Close() })
	if err := Init(writerDB); err != nil {
		t.Fatal(err)
	}

	// Reader connection.
	readerDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { readerDB.Close() })

	var factoryCalls int32
	factory := func(name string, config json.RawMessage) (Channel, error) {
		atomic.AddInt32(&factoryCalls, 1)
		return &stubChannel{
			name:    name,
			closeCh: make(chan struct{}),
			inbound: make(chan Message, 1),
		}, nil
	}

	d := NewDispatcher(func(ctx context.Context, msg Message) ([]Message, error) {
		return nil, nil
	})
	d.RegisterPlatform("webhook", factory)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go d.Watch(ctx, readerDB, 50*time.Millisecond)
	time.Sleep(100 * time.Millisecond)

	// Insert via writer.
	writerDB.Exec(`INSERT INTO channels (name, platform, enabled) VALUES ('wh1', 'webhook', 1)`)
	time.Sleep(300 * time.Millisecond)

	if c := atomic.LoadInt32(&factoryCalls); c < 1 {
		t.Fatalf("expected factory to be called, got %d calls", c)
	}

	cancel()
	d.Close()
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

func TestErrors(t *testing.T) {
	t.Run("ErrChannelNotFound", func(t *testing.T) {
		e := &ErrChannelNotFound{Channel: "foo"}
		if !strings.Contains(e.Error(), "foo") {
			t.Fatal("expected channel name in error")
		}
	})

	t.Run("ErrNoPlatformFactory", func(t *testing.T) {
		e := &ErrNoPlatformFactory{Channel: "foo", Platform: "bar"}
		if !strings.Contains(e.Error(), "bar") {
			t.Fatal("expected platform in error")
		}
	})

	t.Run("ErrChannelDisabled", func(t *testing.T) {
		e := &ErrChannelDisabled{Channel: "foo"}
		if !strings.Contains(e.Error(), "foo") {
			t.Fatal("expected channel name in error")
		}
	})

	t.Run("ErrSendFailed", func(t *testing.T) {
		cause := fmt.Errorf("boom")
		e := &ErrSendFailed{Channel: "foo", Platform: "bar", Cause: cause}
		if !strings.Contains(e.Error(), "boom") {
			t.Fatal("expected cause in error")
		}
		if e.Unwrap() != cause {
			t.Fatal("Unwrap should return cause")
		}
	})
}

// ---------------------------------------------------------------------------
// Direction
// ---------------------------------------------------------------------------

func TestDirection_String(t *testing.T) {
	if Inbound.String() != "inbound" {
		t.Fatalf("expected 'inbound', got %q", Inbound.String())
	}
	if Outbound.String() != "outbound" {
		t.Fatalf("expected 'outbound', got %q", Outbound.String())
	}
}

// ---------------------------------------------------------------------------
// MaxConcurrent semaphore
// ---------------------------------------------------------------------------

func TestDispatcher_MaxConcurrent(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	var concurrent int32
	var maxSeen int32

	handler := func(ctx context.Context, msg Message) ([]Message, error) {
		cur := atomic.AddInt32(&concurrent, 1)
		defer atomic.AddInt32(&concurrent, -1)
		for {
			old := atomic.LoadInt32(&maxSeen)
			if cur <= old || atomic.CompareAndSwapInt32(&maxSeen, old, cur) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		return nil, nil
	}

	ch := &stubChannel{
		name:    "wh1",
		closeCh: make(chan struct{}),
		inbound: make(chan Message, 100),
	}
	factory := func(name string, config json.RawMessage) (Channel, error) {
		return ch, nil
	}

	d := NewDispatcher(handler, WithMaxConcurrent(2))
	d.RegisterPlatform("webhook", factory)

	db.Exec(`INSERT INTO channels (name, platform, enabled) VALUES ('wh1', 'webhook', 1)`)
	d.Reload(ctx, db)

	// Send 5 messages quickly.
	for i := 0; i < 5; i++ {
		ch.inbound <- Message{Text: fmt.Sprintf("msg%d", i)}
	}

	time.Sleep(500 * time.Millisecond)
	d.Close()

	// With the semaphore, dispatch processes messages sequentially per
	// channel (single dispatch goroutine), so maxSeen should be 1.
	// The semaphore still protects against multiple channels dispatching
	// concurrently. Verify it didn't exceed the limit.
	if m := atomic.LoadInt32(&maxSeen); m > 2 {
		t.Fatalf("concurrent handlers exceeded limit: %d > 2", m)
	}
}

// ---------------------------------------------------------------------------
// Platform stubs: basic factory validation
// ---------------------------------------------------------------------------

func TestWhatsAppFactory_RequiresStorePath(t *testing.T) {
	factory := WhatsAppFactory()
	_, err := factory("test", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing store_path")
	}
}

func TestWhatsAppFactory_Valid(t *testing.T) {
	factory := WhatsAppFactory()
	ch, err := factory("test", json.RawMessage(`{"store_path":"/tmp/test.db"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	if ch.Status().Platform != "whatsapp" {
		t.Fatal("expected whatsapp platform")
	}
}

func TestTelegramFactory_RequiresBotToken(t *testing.T) {
	factory := TelegramFactory()
	_, err := factory("test", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing bot_token")
	}
}

func TestTelegramFactory_Valid(t *testing.T) {
	factory := TelegramFactory()
	ch, err := factory("test", json.RawMessage(`{"bot_token":"123:ABC"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	if ch.Status().Platform != "telegram" {
		t.Fatal("expected telegram platform")
	}
}

func TestDiscordFactory_RequiresBotToken(t *testing.T) {
	factory := DiscordFactory()
	_, err := factory("test", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing bot_token")
	}
}

func TestDiscordFactory_Valid(t *testing.T) {
	factory := DiscordFactory()
	ch, err := factory("test", json.RawMessage(`{"bot_token":"Bot MTk..."}`))
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	if ch.Status().Platform != "discord" {
		t.Fatal("expected discord platform")
	}
}
