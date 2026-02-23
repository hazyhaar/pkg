package trace

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// RemoteStore sends trace entries to a BO endpoint via HTTP POST.
// It uses the same async batching pattern as Store: a 1024-capacity channel,
// batches of up to 64, flushed every second.
//
// Usage (FO side):
//
//	rs := trace.NewRemoteStore("https://bo.example.com/api/internal/traces", nil)
//	trace.SetStore(rs)
//	defer rs.Close()
type RemoteStore struct {
	url    string
	client *http.Client
	ch     chan *Entry
	done   chan struct{}
	once   sync.Once
}

// NewRemoteStore creates a RemoteStore that POSTs trace batches to url.
// If client is nil, a default client with 5s timeout is used.
func NewRemoteStore(url string, client *http.Client) *RemoteStore {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	rs := &RemoteStore{
		url:    url,
		client: client,
		ch:     make(chan *Entry, 1024),
		done:   make(chan struct{}),
	}
	go rs.flushLoop()
	return rs
}

// RecordAsync queues an entry for async push. Non-blocking; drops if buffer full.
func (rs *RemoteStore) RecordAsync(e *Entry) {
	select {
	case rs.ch <- e:
	default:
	}
}

// Close drains the buffer and stops the flush goroutine.
func (rs *RemoteStore) Close() error {
	rs.once.Do(func() {
		close(rs.ch)
		<-rs.done
	})
	return nil
}

func (rs *RemoteStore) flushLoop() {
	defer close(rs.done)

	batch := make([]*Entry, 0, 64)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case e, ok := <-rs.ch:
			if !ok {
				rs.flushBatch(batch)
				return
			}
			batch = append(batch, e)
			if len(batch) >= 64 {
				rs.flushBatch(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				rs.flushBatch(batch)
				batch = batch[:0]
			}
		}
	}
}

func (rs *RemoteStore) flushBatch(batch []*Entry) {
	if len(batch) == 0 {
		return
	}

	body, err := json.Marshal(batch)
	if err != nil {
		slog.Error("trace remote: marshal", "error", err)
		return
	}

	resp, err := rs.client.Post(rs.url, "application/json", bytes.NewReader(body))
	if err != nil {
		slog.Error("trace remote: post", "error", err, "url", rs.url, "entries", len(batch))
		return
	}
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		slog.Error("trace remote: post rejected", "status", resp.StatusCode, "entries", len(batch))
	}
}
