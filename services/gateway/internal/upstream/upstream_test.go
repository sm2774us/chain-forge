package upstream

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type fake struct {
	calls map[string]int
	fn    func(url string) (*http.Response, error)
}

func (f *fake) Do(r *http.Request) (*http.Response, error) {
	f.calls[r.URL.String()]++
	return f.fn(r.URL.String())
}

func resp(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body))}
}

func TestRoundRobinAndFailover(t *testing.T) {
	f := &fake{calls: map[string]int{}, fn: func(u string) (*http.Response, error) {
		switch u {
		case "http://bad":
			return nil, errors.New("down")
		case "http://5xx":
			return resp(503, ""), nil
		}
		return resp(200, "ok"), nil
	}}
	now := time.Unix(0, 0)
	p := NewPool([]string{"http://bad", "http://5xx", "http://good"}, f, 1, time.Minute, func() time.Time { return now })
	for i := 0; i < 3; i++ {
		out, err := p.Call(context.Background(), []byte("{}"))
		if err != nil || string(out) != "ok" {
			t.Fatalf("call %d: %v %s", i, err, out)
		}
	}
	// bad + 5xx tripped after first failure → each contacted once, rest skipped.
	if f.calls["http://bad"] != 1 || f.calls["http://5xx"] != 1 || f.calls["http://good"] != 3 {
		t.Fatalf("calls: %v", f.calls)
	}
	h := p.Health()
	if h[0].State != "open" || h[2].State != "closed" {
		t.Fatalf("health: %+v", h)
	}
}

func TestAllDownAndBadURL(t *testing.T) {
	f := &fake{calls: map[string]int{}, fn: func(string) (*http.Response, error) { return nil, errors.New("x") }}
	p := NewPool([]string{"http://a"}, f, 1, time.Hour, nil)
	if _, err := p.Call(context.Background(), nil); !errors.Is(err, ErrNoUpstream) {
		t.Fatal("want ErrNoUpstream")
	}
	if _, err := p.Call(context.Background(), nil); !errors.Is(err, ErrNoUpstream) {
		t.Fatal("open breaker must short-circuit")
	}
	bad := NewPool([]string{"http://bad host\x7f"}, f, 1, time.Hour, nil)
	if _, err := bad.Call(context.Background(), nil); !errors.Is(err, ErrNoUpstream) {
		t.Fatal("bad URL counts as failure")
	}
	empty := NewPool(nil, f, 1, time.Hour, nil)
	if _, err := empty.Call(context.Background(), nil); !errors.Is(err, ErrNoUpstream) {
		t.Fatal("empty pool")
	}
}
