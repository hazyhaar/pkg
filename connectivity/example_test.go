package connectivity_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/hazyhaar/pkg/connectivity"
	_ "modernc.org/sqlite"
)

func Example() {
	// 1. Open an in-memory SQLite database for the routes table.
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := connectivity.Init(db); err != nil {
		log.Fatal(err)
	}

	// 2. Create router and register a local handler.
	router := connectivity.New()
	defer router.Close()

	router.RegisterLocal("billing", func(ctx context.Context, payload []byte) ([]byte, error) {
		return []byte("billed:" + string(payload)), nil
	})

	// 3. Register the HTTP transport factory for remote calls.
	router.RegisterTransport("http", connectivity.HTTPFactory())

	// 4. Configure routes in SQLite: billing runs locally.
	db.Exec(`INSERT INTO routes (service_name, strategy) VALUES ('billing', 'local')`)

	// 5. Load routes.
	if err := router.Reload(context.Background(), db); err != nil {
		log.Fatal(err)
	}

	// 6. Call the service — routed locally.
	resp, err := router.Call(context.Background(), "billing", []byte("$100"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(resp))

	// 7. Switch to noop — disable the service with zero downtime.
	db.Exec(`UPDATE routes SET strategy='noop' WHERE service_name='billing'`)
	router.Reload(context.Background(), db)

	resp, err = router.Call(context.Background(), "billing", []byte("$200"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(resp == nil)

	// Output:
	// billed:$100
	// true
}

func Example_middleware() {
	router := connectivity.New()
	defer router.Close()

	// A handler that echoes the payload.
	echo := func(ctx context.Context, payload []byte) ([]byte, error) {
		return payload, nil
	}

	// Wrap with middleware chain: recovery → timeout → logging.
	wrapped := connectivity.Chain(
		connectivity.Recovery(nil),
		connectivity.Timeout(5*time.Second),
	)(echo)

	resp, err := wrapped(context.Background(), []byte("hello"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(resp))
	// Output:
	// hello
}

func Example_circuitBreaker() {
	cb := connectivity.NewCircuitBreaker(
		connectivity.WithBreakerThreshold(2),
		connectivity.WithBreakerResetTimeout(100*time.Millisecond),
	)

	failingHandler := func(ctx context.Context, payload []byte) ([]byte, error) {
		return nil, fmt.Errorf("service down")
	}

	wrapped := connectivity.WithCircuitBreaker(cb, "payments")(failingHandler)

	// First two calls fail and trip the breaker.
	wrapped(context.Background(), nil)
	wrapped(context.Background(), nil)

	// Third call is rejected by the circuit breaker.
	_, err := wrapped(context.Background(), nil)
	fmt.Println(err)
	// Output:
	// connectivity: circuit open: payments
}

func Example_httpFactory() {
	f := connectivity.HTTPFactory()
	cfg := json.RawMessage(`{"timeout_ms": 5000, "content_type": "application/json"}`)

	handler, closeFn, err := f("https://api.example.com/v1", cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer closeFn()

	_ = handler // Use handler via router.Call or directly.
	fmt.Println("HTTP factory created successfully")
	// Output:
	// HTTP factory created successfully
}
