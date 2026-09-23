// Package webhook delivers event-log records to registered HTTPS endpoints with
// HMAC-SHA256 signatures, bounded retries with exponential backoff, and basic
// SSRF hygiene (private/loopback literal hosts are rejected by default).
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chainforge/gateway/internal/eventlog"
)

// ErrInvalidURL is returned for unusable or unsafe targets.
var ErrInvalidURL = errors.New("webhook: invalid or disallowed url")

// Doer is satisfied by *http.Client.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Registration is one subscriber.
type Registration struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Secret string `json:"-"`
}

// Dispatcher owns registrations and delivery.
type Dispatcher struct {
	mu           sync.Mutex
	regs         map[string]Registration
	seq          int
	client       Doer
	attempts     int
	backoff      time.Duration
	sleep        func(time.Duration)
	allowPrivate bool
}

// Options configure a Dispatcher.
type Options struct {
	Client       Doer
	Attempts     int
	Backoff      time.Duration
	Sleep        func(time.Duration)
	AllowPrivate bool
}

// New builds a Dispatcher.
func New(o Options) *Dispatcher {
	if o.Sleep == nil {
		o.Sleep = time.Sleep
	}
	return &Dispatcher{regs: map[string]Registration{}, client: o.Client, attempts: max(o.Attempts, 1),
		backoff: o.Backoff, sleep: o.Sleep, allowPrivate: o.AllowPrivate}
}

// Sign returns the hex HMAC-SHA256 of body.
func Sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

func (d *Dispatcher) validate(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return ErrInvalidURL
	}
	if d.allowPrivate {
		return nil
	}
	h := u.Hostname()
	if strings.EqualFold(h, "localhost") {
		return ErrInvalidURL
	}
	if ip := net.ParseIP(h); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
		return ErrInvalidURL
	}
	return nil
}

// Register adds a subscriber.
func (d *Dispatcher) Register(rawURL, secret string) (Registration, error) {
	if err := d.validate(rawURL); err != nil {
		return Registration{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seq++
	r := Registration{ID: fmt.Sprintf("wh_%04d", d.seq), URL: rawURL, Secret: secret}
	d.regs[r.ID] = r
	return r, nil
}

// Remove deletes a subscriber.
func (d *Dispatcher) Remove(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.regs[id]
	delete(d.regs, id)
	return ok
}

// List returns registrations sorted by ID.
func (d *Dispatcher) List() []Registration {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Registration, 0, len(d.regs))
	for _, r := range d.regs {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (d *Dispatcher) post(ctx context.Context, r Registration, ev eventlog.Event, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Chainforge-Event", ev.Type)
	req.Header.Set("X-Chainforge-Signature", "sha256="+Sign(r.Secret, body))
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("webhook %s: status %d", r.ID, resp.StatusCode)
	}
	return nil
}

// Deliver fans ev out to all subscribers, returning success/failure counts.
func (d *Dispatcher) Deliver(ctx context.Context, ev eventlog.Event) (ok, failed int) {
	body, _ := json.Marshal(ev)
	for _, r := range d.List() {
		var err error
		wait := d.backoff
		for a := 0; a < d.attempts; a++ {
			if err = d.post(ctx, r, ev, body); err == nil {
				break
			}
			if a < d.attempts-1 {
				d.sleep(wait)
				wait *= 2
			}
		}
		if err == nil {
			ok++
		} else {
			failed++
		}
	}
	return ok, failed
}

// Run consumes the log's live stream until ctx ends.
func (d *Dispatcher) Run(ctx context.Context, l *eventlog.Log) {
	ch, cancel := l.Subscribe(256)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, open := <-ch:
			if !open {
				return
			}
			d.Deliver(ctx, ev)
		}
	}
}
