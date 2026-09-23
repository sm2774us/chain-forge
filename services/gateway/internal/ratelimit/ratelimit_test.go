package ratelimit

import (
	"testing"
	"time"
)

func TestBucket(t *testing.T) {
	now := time.Unix(0, 0)
	l := New(1, 2, func() time.Time { return now })
	for i := 0; i < 2; i++ {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatal("burst denied")
		}
	}
	ok, wait := l.Allow("k")
	if ok || wait != time.Second {
		t.Fatalf("want denial with 1s wait, got %v %v", ok, wait)
	}
	if ok, _ := l.Allow("other"); !ok {
		t.Fatal("keys must be independent")
	}
	now = now.Add(1500 * time.Millisecond)
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("refill failed")
	}
	now = now.Add(time.Hour)
	l.Allow("k")
	if l.buckets["k"].tokens > 2 {
		t.Fatal("burst cap exceeded")
	}
}

func TestDefaultClock(t *testing.T) {
	if ok, _ := New(1, 1, nil).Allow("x"); !ok {
		t.Fatal("first call must pass")
	}
}
