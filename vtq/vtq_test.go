package vtq_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/pkg/dbopen"
	"github.com/hazyhaar/pkg/vtq"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	return dbopen.OpenMemory(t)
}

func newQ(t *testing.T, db *sql.DB, opts vtq.Options) *vtq.Q {
	t.Helper()
	q := vtq.New(db, opts)
	if err := q.EnsureTable(context.Background()); err != nil {
		t.Fatal(err)
	}
	return q
}

func TestPublishAndClaim(t *testing.T) {
	db := openDB(t)
	q := newQ(t, db, vtq.Options{Visibility: time.Second})

	ctx := context.Background()

	if err := q.Publish(ctx, "j1", []byte("hello")); err != nil {
		t.Fatal(err)
	}

	job, err := q.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if job == nil {
		t.Fatal("expected a job")
	}
	if job.ID != "j1" {
		t.Fatalf("got id %q, want j1", job.ID)
	}
	if string(job.Payload) != "hello" {
		t.Fatalf("got payload %q, want hello", string(job.Payload))
	}
	if job.Attempts != 1 {
		t.Fatalf("got attempts %d, want 1", job.Attempts)
	}

	// Second claim returns nil — job is invisible.
	job2, err := q.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if job2 != nil {
		t.Fatal("expected nil, job should be invisible")
	}
}

func TestAck(t *testing.T) {
	db := openDB(t)
	q := newQ(t, db, vtq.Options{Visibility: time.Second})
	ctx := context.Background()

	q.Publish(ctx, "j1", nil)
	job, _ := q.Claim(ctx)
	if err := q.Ack(ctx, job.ID); err != nil {
		t.Fatal(err)
	}

	n, _ := q.Len(ctx)
	if n != 0 {
		t.Fatalf("queue should be empty after ack, got %d", n)
	}
}

func TestNack(t *testing.T) {
	db := openDB(t)
	q := newQ(t, db, vtq.Options{Visibility: 10 * time.Second})
	ctx := context.Background()

	q.Publish(ctx, "j1", []byte("retry-me"))
	job, _ := q.Claim(ctx)

	// Nack makes it visible again immediately.
	if err := q.Nack(ctx, job.ID); err != nil {
		t.Fatal(err)
	}

	job2, err := q.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if job2 == nil {
		t.Fatal("expected job after nack")
	}
	if job2.Attempts != 2 {
		t.Fatalf("got attempts %d, want 2", job2.Attempts)
	}
}

func TestVisibilityTimeout(t *testing.T) {
	db := openDB(t)
	q := newQ(t, db, vtq.Options{Visibility: 50 * time.Millisecond})
	ctx := context.Background()

	q.Publish(ctx, "j1", nil)
	q.Claim(ctx) // claimed, invisible for 50ms

	// Immediately invisible.
	job, _ := q.Claim(ctx)
	if job != nil {
		t.Fatal("job should be invisible")
	}

	// Wait for visibility to expire.
	time.Sleep(80 * time.Millisecond)

	job, err := q.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if job == nil {
		t.Fatal("job should have reappeared")
	}
	if job.Attempts != 2 {
		t.Fatalf("got attempts %d, want 2", job.Attempts)
	}
}

func TestExtend(t *testing.T) {
	db := openDB(t)
	q := newQ(t, db, vtq.Options{Visibility: 50 * time.Millisecond})
	ctx := context.Background()

	q.Publish(ctx, "j1", nil)
	job, _ := q.Claim(ctx)

	// Extend by 500ms — should not reappear after the original 50ms.
	if err := q.Extend(ctx, job.ID, 500*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	time.Sleep(80 * time.Millisecond)

	job2, _ := q.Claim(ctx)
	if job2 != nil {
		t.Fatal("job should still be invisible after extend")
	}
}

func TestMaxAttempts(t *testing.T) {
	db := openDB(t)
	q := newQ(t, db, vtq.Options{
		Visibility:   10 * time.Millisecond,
		PollInterval: 5 * time.Millisecond,
		MaxAttempts:  2,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	q.Publish(ctx, "j1", nil)

	// Claim and nack twice to reach max attempts.
	for i := 0; i < 2; i++ {
		time.Sleep(15 * time.Millisecond)
		job, err := q.Claim(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if job == nil {
			t.Fatalf("expected job on attempt %d", i+1)
		}
		q.Nack(ctx, job.ID)
	}

	// Third attempt: job has attempts=3 > MaxAttempts=2.
	// Run should discard it.
	var handled bool
	var wg sync.WaitGroup
	wg.Add(1)
	runCtx, runCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer runCancel()
	go func() {
		defer wg.Done()
		q.Run(runCtx, func(_ context.Context, j *vtq.Job) error {
			handled = true
			return nil
		})
	}()
	wg.Wait()

	if handled {
		t.Fatal("handler should not have been called — job should be discarded")
	}
	n, _ := q.Len(ctx)
	if n != 0 {
		t.Fatalf("discarded job should be deleted, got len=%d", n)
	}
}

func TestMultipleQueues(t *testing.T) {
	db := openDB(t)
	q1 := newQ(t, db, vtq.Options{Queue: "alpha", Visibility: time.Second})
	q2 := newQ(t, db, vtq.Options{Queue: "beta", Visibility: time.Second})
	ctx := context.Background()

	q1.Publish(ctx, "a1", []byte("alpha"))
	q2.Publish(ctx, "b1", []byte("beta"))

	j1, _ := q1.Claim(ctx)
	j2, _ := q2.Claim(ctx)

	if j1 == nil || j1.ID != "a1" {
		t.Fatal("q1 should get a1")
	}
	if j2 == nil || j2.ID != "b1" {
		t.Fatal("q2 should get b1")
	}

	// q1 should not see q2's job.
	j, _ := q1.Claim(ctx)
	if j != nil {
		t.Fatal("q1 should have no more jobs")
	}
}

func TestRunConsumer(t *testing.T) {
	db := openDB(t)
	q := newQ(t, db, vtq.Options{
		Visibility:   time.Second,
		PollInterval: 10 * time.Millisecond,
	})
	ctx := context.Background()

	q.Publish(ctx, "j1", []byte("one"))
	q.Publish(ctx, "j2", []byte("two"))
	q.Publish(ctx, "j3", []byte("three"))

	var mu sync.Mutex
	var got []string

	runCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	q.Run(runCtx, func(_ context.Context, j *vtq.Job) error {
		mu.Lock()
		got = append(got, j.ID)
		mu.Unlock()
		if len(got) == 3 {
			cancel()
		}
		return nil
	})

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 {
		t.Fatalf("expected 3 jobs, got %d: %v", len(got), got)
	}
}

func TestRunHandlerError(t *testing.T) {
	db := openDB(t)
	q := newQ(t, db, vtq.Options{
		Visibility:   50 * time.Millisecond,
		PollInterval: 10 * time.Millisecond,
	})
	ctx := context.Background()

	q.Publish(ctx, "j1", nil)

	var mu sync.Mutex
	attempts := 0

	runCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	q.Run(runCtx, func(_ context.Context, j *vtq.Job) error {
		mu.Lock()
		attempts++
		a := attempts
		mu.Unlock()
		if a == 1 {
			return errors.New("transient failure")
		}
		cancel()
		return nil
	})

	mu.Lock()
	defer mu.Unlock()
	if attempts < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", attempts)
	}
}

func TestPurge(t *testing.T) {
	db := openDB(t)
	q := newQ(t, db, vtq.Options{})
	ctx := context.Background()

	q.Publish(ctx, "j1", nil)
	q.Publish(ctx, "j2", nil)

	if err := q.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	n, _ := q.Len(ctx)
	if n != 0 {
		t.Fatalf("expected 0 after purge, got %d", n)
	}
}

func TestLeaderElection(t *testing.T) {
	// Demonstrates leader election: 1 row, 2 contenders.
	db := openDB(t)
	q := newQ(t, db, vtq.Options{
		Queue:      "leader",
		Visibility: 100 * time.Millisecond,
	})
	ctx := context.Background()

	// The "leadership token" — a single permanent row.
	q.Publish(ctx, "leader-token", nil)

	// Instance A claims leadership.
	jobA, _ := q.Claim(ctx)
	if jobA == nil {
		t.Fatal("instance A should become leader")
	}

	// Instance B cannot claim — leader is active.
	jobB, _ := q.Claim(ctx)
	if jobB != nil {
		t.Fatal("instance B should NOT get leadership while A holds it")
	}

	// A crashes (simulated by letting visibility expire).
	time.Sleep(120 * time.Millisecond)

	// B takes over.
	jobB, _ = q.Claim(ctx)
	if jobB == nil {
		t.Fatal("instance B should take over after A's timeout")
	}
}

// --- Batch tests ---

func TestBatchClaim(t *testing.T) {
	db := openDB(t)
	q := newQ(t, db, vtq.Options{Visibility: time.Second})
	ctx := context.Background()

	for i := range 5 {
		q.Publish(ctx, fmt.Sprintf("j%d", i+1), []byte(fmt.Sprintf("payload-%d", i+1)))
	}

	jobs, err := q.BatchClaim(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(jobs))
	}

	// Verify remaining visible.
	remaining, _ := q.Len(ctx)
	if remaining != 5 {
		t.Fatalf("total jobs should still be 5, got %d", remaining)
	}

	// Claim again — should get the remaining 2.
	jobs2, err := q.BatchClaim(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs2) != 2 {
		t.Fatalf("expected 2 remaining jobs, got %d", len(jobs2))
	}
}

func TestBatchClaimEmpty(t *testing.T) {
	db := openDB(t)
	q := newQ(t, db, vtq.Options{Visibility: time.Second})
	ctx := context.Background()

	jobs, err := q.BatchClaim(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if jobs == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(jobs) != 0 {
		t.Fatalf("expected 0 jobs, got %d", len(jobs))
	}
}

func TestBatchClaimMoreThanAvailable(t *testing.T) {
	db := openDB(t)
	q := newQ(t, db, vtq.Options{Visibility: time.Second})
	ctx := context.Background()

	q.Publish(ctx, "j1", nil)
	q.Publish(ctx, "j2", nil)

	jobs, err := q.BatchClaim(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}
}

func TestRunBatch(t *testing.T) {
	db := openDB(t)
	q := newQ(t, db, vtq.Options{
		Visibility:   5 * time.Second,
		PollInterval: 10 * time.Millisecond,
	})
	ctx := context.Background()

	const total = 10
	for i := range total {
		q.Publish(ctx, fmt.Sprintf("j%d", i+1), nil)
	}

	var processed atomic.Int32

	runCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	q.RunBatch(runCtx, 5, 3, func(_ context.Context, j *vtq.Job) error {
		if processed.Add(1) >= total {
			cancel()
		}
		return nil
	})

	got := int(processed.Load())
	if got != total {
		t.Fatalf("expected %d processed, got %d", total, got)
	}

	n, _ := q.Len(ctx)
	if n != 0 {
		t.Fatalf("expected 0 remaining, got %d", n)
	}
}

func TestRunBatchConcurrency(t *testing.T) {
	db := openDB(t)
	q := newQ(t, db, vtq.Options{
		Visibility:   5 * time.Second,
		PollInterval: 10 * time.Millisecond,
	})
	ctx := context.Background()

	const total = 10
	const maxConc = 2

	for i := range total {
		q.Publish(ctx, fmt.Sprintf("j%d", i+1), nil)
	}

	var current atomic.Int32
	var peak atomic.Int32
	var processed atomic.Int32

	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	q.RunBatch(runCtx, 5, maxConc, func(_ context.Context, j *vtq.Job) error {
		c := current.Add(1)
		// Update peak atomically.
		for {
			p := peak.Load()
			if c <= p || peak.CompareAndSwap(p, c) {
				break
			}
		}

		time.Sleep(20 * time.Millisecond) // simulate work

		current.Add(-1)
		if processed.Add(1) >= total {
			cancel()
		}
		return nil
	})

	got := int(processed.Load())
	if got != total {
		t.Fatalf("expected %d processed, got %d", total, got)
	}

	p := int(peak.Load())
	if p > maxConc {
		t.Fatalf("peak concurrency = %d, exceeds maxConcurrency = %d", p, maxConc)
	}
}
