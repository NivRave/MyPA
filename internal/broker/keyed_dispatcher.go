package broker

import (
	"log/slog"
	"runtime/debug"
	"sync"
)

// keyedDispatcher runs tasks concurrently across keys and strictly in submission order within a
// key (P2-8): one user's long turn no longer blocks other users, and a user's own messages are
// never processed out of order or in parallel. A key's worker goroutine exists only while it
// has queued work; presence of the key in `queues` means "a worker is running".
type keyedDispatcher struct {
	mu     sync.Mutex
	queues map[string][]func()
	wg     sync.WaitGroup
}

func newKeyedDispatcher() *keyedDispatcher {
	return &keyedDispatcher{queues: make(map[string][]func())}
}

// Submit enqueues task for key, starting a worker for the key if none is running.
func (d *keyedDispatcher) Submit(key string, task func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	q, running := d.queues[key]
	d.queues[key] = append(q, task)
	if !running {
		d.wg.Add(1)
		go d.drain(key)
	}
}

func (d *keyedDispatcher) drain(key string) {
	defer d.wg.Done()
	for {
		d.mu.Lock()
		q := d.queues[key]
		if len(q) == 0 {
			delete(d.queues, key) // worker exits; the next Submit for key starts a new one
			d.mu.Unlock()
			return
		}
		task := q[0]
		q[0] = nil // release the closure for GC
		d.queues[key] = q[1:]
		d.mu.Unlock()
		runContained(key, task)
	}
}

// runContained runs task, containing panics so one bad message cannot kill the key's worker.
func runContained(key string, task func()) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("recovered panic in consumer task", "key", key, "panic", r, "stack", string(debug.Stack()))
		}
	}()
	task()
}

// Wait blocks until every queued task has run (used before reconnecting or closing).
func (d *keyedDispatcher) Wait() { d.wg.Wait() }

// activeKeys reports how many keys currently have a running worker (tests).
func (d *keyedDispatcher) activeKeys() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.queues)
}

// deliveryKey is the ordering key for inbound messages: one serial lane per sender.
func deliveryKey(source, userID string) string { return source + ":" + userID }
