package broker

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tasks for the same key run strictly in submission order, never concurrently.
func TestKeyedDispatcher_OrderedWithinKey(t *testing.T) {
	d := newKeyedDispatcher()
	var mu sync.Mutex
	var got []int
	var running int32
	for i := 0; i < 200; i++ {
		i := i
		d.Submit("user-a", func() {
			assert.Equal(t, int32(1), atomic.AddInt32(&running, 1), "same-key tasks must not overlap")
			mu.Lock()
			got = append(got, i)
			mu.Unlock()
			atomic.AddInt32(&running, -1)
		})
	}
	d.Wait()
	require.Len(t, got, 200)
	for i := range got {
		assert.Equal(t, i, got[i])
	}
}

// A blocked key must not delay other keys.
func TestKeyedDispatcher_ParallelAcrossKeys(t *testing.T) {
	d := newKeyedDispatcher()
	release := make(chan struct{})
	bDone := make(chan struct{})

	d.Submit("user-a", func() { <-release }) // A is stuck until B has finished
	d.Submit("user-b", func() { close(bDone) })

	select {
	case <-bDone:
	case <-time.After(2 * time.Second):
		t.Fatal("user B was blocked behind user A")
	}
	close(release)
	d.Wait()
}

// After a key's queue drains, its worker exits; later tasks for that key start a new worker.
func TestKeyedDispatcher_WorkerRestartsAfterDrain(t *testing.T) {
	d := newKeyedDispatcher()
	var n int32
	d.Submit("k", func() { atomic.AddInt32(&n, 1) })
	d.Wait()
	assert.Equal(t, 0, d.activeKeys(), "idle keys must not leak goroutines")
	d.Submit("k", func() { atomic.AddInt32(&n, 1) })
	d.Wait()
	assert.Equal(t, int32(2), atomic.LoadInt32(&n))
}

// A panicking task must not kill the worker or the process; later tasks still run.
func TestKeyedDispatcher_PanicIsContained(t *testing.T) {
	d := newKeyedDispatcher()
	var ran int32
	d.Submit("k", func() { panic("boom") })
	d.Submit("k", func() { atomic.AddInt32(&ran, 1) })
	d.Wait()
	assert.Equal(t, int32(1), atomic.LoadInt32(&ran))
}

func TestDeliveryKey(t *testing.T) {
	assert.Equal(t, "telegram:42", deliveryKey("telegram", "42"))
	assert.Equal(t, "whatsapp:+9725", deliveryKey("whatsapp", "+9725"))
	assert.Equal(t, ":", deliveryKey("", ""), "unknown sender still gets a deterministic key")
}
