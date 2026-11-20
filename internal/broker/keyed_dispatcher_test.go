package broker

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/channel"
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

// V4-32 (ADR 0002 §9): one processing lane per person, whichever channel a message comes from;
// a sender the lane function does not know keeps its own channel-and-sender lane, which a
// person's lane can never collide with.
func TestConsumer_LanePerPerson(t *testing.T) {
	persons := map[string]string{"telegram|42": "p-1", "whatsapp|+9725": "p-1", "telegram|43": "p-2"}
	c := &Consumer{laneOf: func(m channel.Inbound) string { return persons[m.Channel+"|"+m.Sender] }}
	tg := c.lane(channel.Inbound{Channel: "telegram", Sender: "42"})
	wa := c.lane(channel.Inbound{Channel: "whatsapp", Sender: "+9725"})
	other := c.lane(channel.Inbound{Channel: "telegram", Sender: "43"})
	unknown := c.lane(channel.Inbound{Channel: "telegram", Sender: "99"})
	assert.Equal(t, tg, wa, "one person, one lane across channels")
	assert.NotEqual(t, tg, other)
	assert.Equal(t, "telegram:99", unknown, "an unknown sender keeps one lane per channel and sender")
	assert.NotEqual(t, "telegram:99", c.lane(channel.Inbound{Channel: "x", Sender: "y"}))
	assert.Equal(t, "telegram:42", (&Consumer{}).lane(channel.Inbound{Channel: "telegram", Sender: "42"}), "without a lane function, as before")
}
