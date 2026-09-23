package webhook

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainforge/gateway/internal/eventlog"
)

type rec struct {
	mu      sync.Mutex
	status  []int // popped per call; last repeats
	err     error
	headers []http.Header
	bodies  []string
}

func (r *rec) Do(q *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, _ := io.ReadAll(q.Body)
	r.headers = append(r.headers, q.Header)
	r.bodies = append(r.bodies, string(b))
	if r.err != nil {
		return nil, r.err
	}
	code := r.status[0]
	if len(r.status) > 1 {
		r.status = r.status[1:]
	}
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(""))}, nil
}

func TestRegisterValidation(t *testing.T) {
	d := New(Options{})
	for _, u := range []string{"ftp://x.io", "http://localhost/x", "http://127.0.0.1/x", "http://10.0.0.5/x", "http://169.254.1.1/", "http://0.0.0.0/", "https://", "://bad"} {
		if _, err := d.Register(u, "s"); !errors.Is(err, ErrInvalidURL) {
			t.Errorf("%s accepted", u)
		}
	}
	r, err := d.Register("https://example.com/hook", "s")
	if err != nil || r.ID != "wh_0001" {
		t.Fatal(err, r)
	}
	if _, err := New(Options{AllowPrivate: true}).Register("http://localhost:9/x", "s"); err != nil {
		t.Fatal("allowPrivate")
	}
	if len(d.List()) != 1 || !d.Remove(r.ID) || d.Remove(r.ID) {
		t.Fatal("list/remove")
	}
}

func TestDeliverSignsAndRetries(t *testing.T) {
	c := &rec{status: []int{500, 500, 204}}
	var slept []time.Duration
	d := New(Options{Client: c, Attempts: 3, Backoff: time.Second, Sleep: func(x time.Duration) { slept = append(slept, x) }})
	_, _ = d.Register("https://a.example/h", "topsecret")
	ok, failed := d.Deliver(context.Background(), eventlog.Event{Type: "block.new", Key: "chain"})
	if ok != 1 || failed != 0 || len(slept) != 2 || slept[1] != 2*time.Second {
		t.Fatalf("ok=%d failed=%d slept=%v", ok, failed, slept)
	}
	last := len(c.bodies) - 1
	if got := c.headers[last].Get("X-Chainforge-Signature"); got != "sha256="+Sign("topsecret", []byte(c.bodies[last])) {
		t.Fatal("bad signature", got)
	}
	if c.headers[last].Get("X-Chainforge-Event") != "block.new" {
		t.Fatal("event header")
	}
}

func TestDeliverFailureAndBadRequest(t *testing.T) {
	c := &rec{err: errors.New("net")}
	d := New(Options{Client: c, Attempts: 2, Sleep: func(time.Duration) {}})
	_, _ = d.Register("https://a.example/h", "s")
	if ok, failed := d.Deliver(context.Background(), eventlog.Event{}); ok != 0 || failed != 1 {
		t.Fatal("must count failure")
	}
	d.regs["x"] = Registration{ID: "x", URL: "http://bad host\x7f"}
	if _, failed := d.Deliver(context.Background(), eventlog.Event{}); failed != 2 {
		t.Fatal("unbuildable request is a failure")
	}
}

func TestRunDeliversLiveEvents(t *testing.T) {
	c := &rec{status: []int{200}}
	d := New(Options{Client: c})
	_, _ = d.Register("https://a.example/h", "s")
	l := eventlog.New(1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx, l); close(done) }()
	for i := 0; ; i++ { // subscribe happens inside Run; retry until delivered
		_, _ = l.Append("k", "t", i)
		c.mu.Lock()
		n := len(c.bodies)
		c.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
}

func TestRunStopsWhenEvicted(t *testing.T) {
	d := New(Options{Client: &rec{status: []int{200}}})
	l := eventlog.New(1)
	block := make(chan struct{})
	d.client = blockingDoer{block}
	_, _ = d.Register("https://a.example/h", "s")
	done := make(chan struct{})
	go func() { d.Run(context.Background(), l); close(done) }()
	for i := 0; i < 2000; i++ { // overflow the 256 buffer while delivery is blocked
		_, _ = l.Append("k", "t", i)
		time.Sleep(10 * time.Microsecond)
	}
	close(block)
	<-done
}

type blockingDoer struct{ ch chan struct{} }

func (b blockingDoer) Do(*http.Request) (*http.Response, error) {
	<-b.ch
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
}
