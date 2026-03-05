package vtq_test

import (
	"context"
	"testing"
	"time"

	"github.com/hazyhaar/pkg/dbopen"
	"github.com/hazyhaar/pkg/vtq"
)

func TestCrashRecovery_UnackedJobReappears(t *testing.T) {
	// Simulate HORAG pipeline crash: job Claimed but never Acked.
	// After visibility timeout expires, job must reappear for retry.
	db := dbopen.OpenMemory(t)
	q := vtq.New(db, vtq.Options{
		Queue:      "horag",
		Visibility: 50 * time.Millisecond,
	})
	if err := q.EnsureTable(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// Enqueue a pipeline task
	if err := q.Publish(ctx, "doc-001", []byte(`{"path":"buffer/pending/doc.md"}`)); err != nil {
		t.Fatal(err)
	}

	// Consumer A claims it (simulates pipeline starting processing)
	job, err := q.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if job == nil {
		t.Fatal("expected a job")
	}
	if job.Attempts != 1 {
		t.Fatalf("expected attempts=1, got %d", job.Attempts)
	}

	// Consumer A "crashes" — no Ack, no Nack

	// Immediately: job should be invisible
	job2, err := q.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if job2 != nil {
		t.Fatal("job should be invisible during processing")
	}

	// Wait for visibility timeout
	time.Sleep(80 * time.Millisecond)

	// Consumer B picks up the job (simulates HORAG restart)
	job3, err := q.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if job3 == nil {
		t.Fatal("job should have reappeared after crash")
	}
	if job3.ID != "doc-001" {
		t.Fatalf("expected job doc-001, got %s", job3.ID)
	}
	if job3.Attempts != 2 {
		t.Fatalf("expected attempts=2, got %d", job3.Attempts)
	}

	// Payload must be intact
	if string(job3.Payload) != `{"path":"buffer/pending/doc.md"}` {
		t.Fatalf("payload corrupted: %s", job3.Payload)
	}

	// Successful processing: Ack
	if err := q.Ack(ctx, job3.ID); err != nil {
		t.Fatal(err)
	}

	// No more jobs
	job4, err := q.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if job4 != nil {
		t.Fatal("job should be gone after Ack")
	}
}

func TestCrashRecovery_MaxAttemptsExhaustion(t *testing.T) {
	// MaxAttempts enforcement is done by Run()/poll(), not Claim().
	// When attempts > MaxAttempts, poll() silently discards (Acks) the job.
	// Here we use Run with a handler that always fails to simulate repeated crashes.
	db := dbopen.OpenMemory(t)
	q := vtq.New(db, vtq.Options{
		Queue:        "horag",
		Visibility:   30 * time.Millisecond,
		PollInterval: 20 * time.Millisecond,
		MaxAttempts:  3,
	})
	if err := q.EnsureTable(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := q.Publish(ctx, "bad-doc", []byte(`crash`)); err != nil {
		t.Fatal(err)
	}

	// Run consumer with a handler that always fails (simulates crashes)
	attempts := 0
	runCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	q.Run(runCtx, func(ctx context.Context, job *vtq.Job) error {
		attempts++
		return context.DeadlineExceeded // simulate failure
	})

	// Handler should have been called MaxAttempts times (3), then job discarded.
	if attempts != 3 {
		t.Fatalf("expected handler called %d times, got %d", 3, attempts)
	}

	// Queue should be empty
	n, err := q.Len(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected 0 remaining jobs, got %d", n)
	}
}

func TestCrashRecovery_NackRedelivery(t *testing.T) {
	// Explicit Nack (handler error) should make job immediately reappear.
	db := dbopen.OpenMemory(t)
	q := vtq.New(db, vtq.Options{
		Queue:      "horag",
		Visibility: 5 * time.Second, // long timeout
	})
	if err := q.EnsureTable(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := q.Publish(ctx, "retry-doc", []byte(`data`)); err != nil {
		t.Fatal(err)
	}

	// Claim
	job, err := q.Claim(ctx)
	if err != nil || job == nil {
		t.Fatal("expected job")
	}

	// Nack (simulates handler returning error)
	if err := q.Nack(ctx, job.ID); err != nil {
		t.Fatal(err)
	}

	// Should be immediately available (Nack resets visibility)
	job2, err := q.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if job2 == nil {
		t.Fatal("job should be available immediately after Nack")
	}
	if job2.Attempts != 2 {
		t.Fatalf("expected attempts=2 after Nack, got %d", job2.Attempts)
	}
}
