// Package eventlog is a partitioned, append-only log with Kafka semantics:
// keyed partitioning (per-key ordering), per-partition offsets, consumer-group
// commits and live subscriptions with slow-consumer eviction.
package eventlog

import (
	"encoding/json"
	"hash/fnv"
	"sync"
)

// Event is one record.
type Event struct {
	Partition int             `json:"partition"`
	Offset    uint64          `json:"offset"`
	Key       string          `json:"key"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// Log is safe for concurrent use.
type Log struct {
	mu      sync.RWMutex
	parts   [][]Event
	commits map[string]uint64
	subs    map[int]chan Event
	nextSub int
}

// New creates a log with n partitions (min 1).
func New(n int) *Log {
	n = max(n, 1)
	return &Log{parts: make([][]Event, n), commits: map[string]uint64{}, subs: map[int]chan Event{}}
}

// Append serialises payload and appends it to the partition owned by key.
func (l *Log) Append(key, typ string, payload any) (Event, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Event{}, err
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	l.mu.Lock()
	defer l.mu.Unlock()
	p := int(h.Sum32() % uint32(len(l.parts)))
	ev := Event{Partition: p, Offset: uint64(len(l.parts[p])), Key: key, Type: typ, Payload: raw}
	l.parts[p] = append(l.parts[p], ev)
	for id, ch := range l.subs {
		select {
		case ch <- ev:
		default: // slow consumer: evict rather than block producers
			close(ch)
			delete(l.subs, id)
		}
	}
	return ev, nil
}

// Read returns up to max events from a partition starting at offset from.
func (l *Log) Read(part int, from uint64, limit int) []Event {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if part < 0 || part >= len(l.parts) || from >= uint64(len(l.parts[part])) {
		return nil
	}
	end := min(uint64(len(l.parts[part])), from+uint64(max(limit, 0)))
	return append([]Event(nil), l.parts[part][from:end]...)
}

func ck(group string, part int) string { return group + "/" + string(rune('0'+part)) }

// Commit stores a group's next-offset for a partition.
func (l *Log) Commit(group string, part int, next uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.commits[ck(group, part)] = next
}

// Committed returns a group's next-offset (0 if none).
func (l *Log) Committed(group string, part int) uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.commits[ck(group, part)]
}

// Subscribe returns a live channel and a cancel func. The channel is closed if
// the consumer falls behind by more than buf events.
func (l *Log) Subscribe(buf int) (<-chan Event, func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	id := l.nextSub
	l.nextSub++
	ch := make(chan Event, max(buf, 1))
	l.subs[id] = ch
	return ch, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if c, ok := l.subs[id]; ok {
			close(c)
			delete(l.subs, id)
		}
	}
}
