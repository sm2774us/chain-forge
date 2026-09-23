// Package upstream load-balances JSON-RPC calls across node endpoints with a
// per-endpoint circuit breaker and automatic failover.
package upstream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/chainforge/gateway/internal/breaker"
)

// ErrNoUpstream means every endpoint failed or is circuit-broken.
var ErrNoUpstream = errors.New("upstream: no healthy endpoint")

// Doer is satisfied by *http.Client; tests inject fakes.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

type endpoint struct {
	url string
	br  *breaker.Breaker
}

// Pool is a round-robin pool with failover.
type Pool struct {
	eps    []*endpoint
	next   atomic.Uint64
	client Doer
}

// Status describes one endpoint for health reporting.
type Status struct {
	URL   string `json:"url"`
	State string `json:"state"`
}

// NewPool builds a pool. threshold/cooldown configure each breaker.
func NewPool(urls []string, client Doer, threshold int, cooldown time.Duration, now func() time.Time) *Pool {
	p := &Pool{client: client}
	for _, u := range urls {
		p.eps = append(p.eps, &endpoint{url: u, br: breaker.New(threshold, cooldown, now)})
	}
	return p
}

// Call POSTs body to endpoints in round-robin order until one answers < 500.
func (p *Pool) Call(ctx context.Context, body []byte) ([]byte, error) {
	start := int(p.next.Add(1)-1) % max(len(p.eps), 1)
	for i := range p.eps {
		ep := p.eps[(start+i)%len(p.eps)]
		if !ep.br.Allow() {
			continue
		}
		out, err := p.try(ctx, ep, body)
		if err != nil {
			ep.br.Failure()
			continue
		}
		ep.br.Success()
		return out, nil
	}
	return nil, ErrNoUpstream
}

func (p *Pool) try(ctx context.Context, ep *endpoint, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("upstream %s: status %d", ep.url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// Health reports breaker state per endpoint.
func (p *Pool) Health() []Status {
	out := make([]Status, len(p.eps))
	for i, e := range p.eps {
		out[i] = Status{URL: e.url, State: e.br.State().String()}
	}
	return out
}
