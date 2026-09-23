package eventlog

import "testing"

func TestAppendReadOrdering(t *testing.T) {
	l := New(4)
	var part int
	for i := 0; i < 3; i++ {
		ev, err := l.Append("same-key", "t", i)
		if err != nil || ev.Offset != uint64(i) {
			t.Fatalf("offset %d: %+v %v", i, ev, err)
		}
		part = ev.Partition
	}
	got := l.Read(part, 1, 10)
	if len(got) != 2 || got[0].Offset != 1 {
		t.Fatalf("read: %+v", got)
	}
	if len(l.Read(part, 0, 2)) != 2 || l.Read(part, 9, 1) != nil || l.Read(-1, 0, 1) != nil || l.Read(99, 0, 1) != nil || len(l.Read(part, 0, -5)) != 0 {
		t.Fatal("bounds")
	}
}

func TestAppendMarshalError(t *testing.T) {
	if _, err := New(0).Append("k", "t", make(chan int)); err == nil {
		t.Fatal("want marshal error")
	}
}

func TestPartitionSpread(t *testing.T) {
	l := New(3)
	seen := map[int]bool{}
	for _, k := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		ev, _ := l.Append(k, "t", k)
		seen[ev.Partition] = true
	}
	if len(seen) < 2 {
		t.Fatal("keys should spread across partitions")
	}
}

func TestCommits(t *testing.T) {
	l := New(1)
	if l.Committed("g", 0) != 0 {
		t.Fatal("default 0")
	}
	l.Commit("g", 0, 5)
	if l.Committed("g", 0) != 5 || l.Committed("other", 0) != 0 {
		t.Fatal("commit isolation")
	}
}

func TestSubscribeAndEviction(t *testing.T) {
	l := New(1)
	ch, cancel := l.Subscribe(1)
	_, _ = l.Append("k", "t", 1)
	if (<-ch).Offset != 0 {
		t.Fatal("live event")
	}
	_, _ = l.Append("k", "t", 2)
	_, _ = l.Append("k", "t", 3) // buffer full → evicted
	<-ch
	if _, ok := <-ch; ok {
		t.Fatal("slow consumer must be closed")
	}
	cancel() // idempotent after eviction
	ch2, cancel2 := l.Subscribe(0)
	cancel2()
	if _, ok := <-ch2; ok {
		t.Fatal("cancel closes channel")
	}
}
