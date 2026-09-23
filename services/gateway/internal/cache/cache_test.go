package cache

import (
	"testing"
	"time"
)

func TestLRUAndTTL(t *testing.T) {
	now := time.Unix(0, 0)
	c := New(2, time.Minute, func() time.Time { return now })
	if _, ok := c.Get("a"); ok {
		t.Fatal("miss expected")
	}
	c.Set("a", []byte("1"))
	c.Set("b", []byte("2"))
	c.Get("a") // a is now MRU
	c.Set("c", []byte("3"))
	if _, ok := c.Get("b"); ok {
		t.Fatal("b should be evicted")
	}
	c.Set("a", []byte("9")) // overwrite
	if v, _ := c.Get("a"); string(v) != "9" || c.Len() != 2 {
		t.Fatal("overwrite")
	}
	now = now.Add(2 * time.Minute)
	if _, ok := c.Get("a"); ok || c.Len() != 1 {
		t.Fatal("expired entry must be dropped")
	}
}

func TestDefaultClock(t *testing.T) {
	c := New(1, time.Hour, nil)
	c.Set("k", []byte("v"))
	if _, ok := c.Get("k"); !ok {
		t.Fatal("hit expected")
	}
}
