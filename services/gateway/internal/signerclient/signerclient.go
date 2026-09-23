// Package signerclient is the Go side of the Go↔Rust custody boundary. The
// gateway never sees private keys: it forwards sign requests to the Rust signer
// over HTTP, authenticated with HMAC-SHA256 over "<unix-ts>.<body>" (the signer
// enforces a ±30s window to bound replay).
package signerclient

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Doer is satisfied by *http.Client.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Client talks to the signer.
type Client struct {
	BaseURL string
	Secret  string
	HTTP    Doer
	Now     func() time.Time
}

// SignRequest is the wire request.
type SignRequest struct {
	KeyID   string `json:"key_id"`
	Message string `json:"message"`
}

// SignResponse is the wire response.
type SignResponse struct {
	Address   string `json:"address"`
	Digest    string `json:"digest"`
	Signature string `json:"signature"`
}

// APIError carries the signer's HTTP status and message.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("signer %d: %s", e.Status, e.Message) }

// MAC computes the request authenticator.
func MAC(secret, ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts + "."))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// TxRequest asks the signer to sign an EIP-155 legacy transaction. Numeric
// fields that can exceed 64 bits travel as decimal strings, never floats.
type TxRequest struct {
	KeyID    string `json:"key_id"`
	ChainID  uint64 `json:"chain_id"`
	Nonce    uint64 `json:"nonce"`
	GasPrice string `json:"gas_price"`
	GasLimit uint64 `json:"gas_limit"`
	To       string `json:"to"`
	Value    string `json:"value"`
	Data     string `json:"data"`
}

// TxResponse is the signed transaction.
type TxResponse struct {
	Address     string `json:"address"`
	SigningHash string `json:"signing_hash"`
	RawTx       string `json:"raw_tx"`
	TxHash      string `json:"tx_hash"`
}

func (c *Client) post(ctx context.Context, path string, in, out any) error {
	body, _ := json.Marshal(in)
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	ts := strconv.FormatInt(now().Unix(), 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Signature", MAC(c.Secret, ts, body))
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return &APIError{resp.StatusCode, e.Error}
	}
	return json.Unmarshal(raw, out)
}

// Sign requests an EIP-191 personal_sign signature.
func (c *Client) Sign(ctx context.Context, sr SignRequest) (SignResponse, error) {
	var out SignResponse
	err := c.post(ctx, "/v1/sign", sr, &out)
	return out, err
}

// SignTx requests a policy-checked transaction signature.
func (c *Client) SignTx(ctx context.Context, tr TxRequest) (TxResponse, error) {
	var out TxResponse
	err := c.post(ctx, "/v1/sign-tx", tr, &out)
	return out, err
}
