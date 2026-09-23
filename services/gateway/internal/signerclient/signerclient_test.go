package signerclient

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
	code int
	body string
	err  error
	req  *http.Request
	sent string
}

func (f *fake) Do(r *http.Request) (*http.Response, error) {
	f.req = r
	b, _ := io.ReadAll(r.Body)
	f.sent = string(b)
	if f.err != nil {
		return nil, f.err
	}
	return &http.Response{StatusCode: f.code, Body: io.NopCloser(strings.NewReader(f.body))}, nil
}

type errBody struct{}

func (errBody) Read([]byte) (int, error) { return 0, errors.New("read") }
func (errBody) Close() error             { return nil }

type badRead struct{}

func (badRead) Do(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: errBody{}}, nil
}

func TestSignOK(t *testing.T) {
	f := &fake{code: 200, body: `{"address":"0xa","digest":"0xd","signature":"0xs"}`}
	c := &Client{BaseURL: "http://s", Secret: "k", HTTP: f, Now: func() time.Time { return time.Unix(1000, 0) }}
	out, err := c.Sign(context.Background(), SignRequest{KeyID: "alice", Message: "hi"})
	if err != nil || out.Signature != "0xs" {
		t.Fatal(err, out)
	}
	if f.req.Header.Get("X-Timestamp") != "1000" || f.req.Header.Get("X-Signature") != MAC("k", "1000", []byte(f.sent)) {
		t.Fatal("auth headers")
	}
}

func TestSignErrors(t *testing.T) {
	ctx := context.Background()
	c := &Client{BaseURL: "http://s", HTTP: &fake{code: 403, body: `{"error":"nope"}`}}
	var ae *APIError
	if _, err := c.Sign(ctx, SignRequest{}); !errors.As(err, &ae) || ae.Status != 403 || !strings.Contains(ae.Error(), "nope") {
		t.Fatal("api error", err)
	}
	if _, err := (&Client{BaseURL: "http://s", HTTP: &fake{err: errors.New("net")}}).Sign(ctx, SignRequest{}); err == nil {
		t.Fatal("transport")
	}
	if _, err := (&Client{BaseURL: "http://s", HTTP: badRead{}}).Sign(ctx, SignRequest{}); err == nil {
		t.Fatal("read")
	}
	if _, err := (&Client{BaseURL: "http://s", HTTP: &fake{code: 200, body: "{"}}).Sign(ctx, SignRequest{}); err == nil {
		t.Fatal("decode")
	}
	if _, err := (&Client{BaseURL: "http://bad\x7f", HTTP: &fake{}}).Sign(ctx, SignRequest{}); err == nil {
		t.Fatal("bad url")
	}
	if _, err := (&Client{BaseURL: "http://s", HTTP: &fake{code: 200, body: "{}"}}).Sign(ctx, SignRequest{}); err != nil {
		t.Fatal("default clock path")
	}
}

func TestSignTxOK(t *testing.T) {
	f := &fake{code: 200, body: `{"address":"0xa","signing_hash":"0xh","raw_tx":"0xr","tx_hash":"0xt"}`}
	c := &Client{BaseURL: "http://s", Secret: "k", HTTP: f, Now: func() time.Time { return time.Unix(1000, 0) }}
	out, err := c.SignTx(context.Background(), TxRequest{KeyID: "alice", ChainID: 1337, To: "0x1", Value: "5"})
	if err != nil || out.RawTx != "0xr" || out.TxHash != "0xt" {
		t.Fatal(err, out)
	}
	if f.req.URL.Path != "/v1/sign-tx" || f.req.Header.Get("X-Signature") != MAC("k", "1000", []byte(f.sent)) {
		t.Fatal("path or auth headers")
	}
	var ae *APIError
	c.HTTP = &fake{code: 403, body: `{"error":"chain id not allowed"}`}
	if _, err := c.SignTx(context.Background(), TxRequest{}); !errors.As(err, &ae) || ae.Status != 403 {
		t.Fatal(err)
	}
}
