package breaker

import (
	"testing"
	"time"
)

func TestLifecycle(t *testing.T) {
	now := time.Unix(0, 0)
	b := New(2, time.Second, func() time.Time { return now })
	if !b.Allow() || b.State() != Closed {
		t.Fatal("starts closed")
	}
	b.Failure()
	b.Failure()
	if b.State() != Open || b.Allow() {
		t.Fatal("must trip open")
	}
	now = now.Add(2 * time.Second)
	if !b.Allow() || b.State() != HalfOpen {
		t.Fatal("probe admitted after cooldown")
	}
	if b.Allow() {
		t.Fatal("only one probe in half-open")
	}
	b.Failure()
	if b.State() != Open {
		t.Fatal("failed probe reopens")
	}
	now = now.Add(2 * time.Second)
	b.Allow()
	b.Success()
	if b.State() != Closed || !b.Allow() {
		t.Fatal("successful probe closes")
	}
}

func TestHalfOpenReprobe(t *testing.T) {
	b := New(1, 0, nil)
	b.Failure()
	b.Allow() // -> half-open probe
	b.mu.Lock()
	b.probing = false
	b.mu.Unlock()
	if !b.Allow() {
		t.Fatal("re-probe when previous probe slot freed")
	}
}

func TestStateString(t *testing.T) {
	if Closed.String() != "closed" || Open.String() != "open" || HalfOpen.String() != "half-open" {
		t.Fatal("names")
	}
}
