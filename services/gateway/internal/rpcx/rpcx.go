// Package rpcx is a tiny typed JSON-RPC 2.0 caller shared by the relay,
// Solana source and intent packages.
package rpcx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Caller sends a raw JSON-RPC body and returns the raw response (upstream.Pool).
type Caller interface {
	Call(ctx context.Context, body []byte) ([]byte, error)
}

// ErrNull means the node answered `result: null`.
var ErrNull = errors.New("rpcx: null result")

// Error is a JSON-RPC error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// Call invokes method and decodes `result` into out (out may be nil).
func Call(ctx context.Context, c Caller, method string, params []any, out any) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return err
	}
	raw, err := c.Call(ctx, body)
	if err != nil {
		return err
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *Error          `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("rpcx: malformed response: %w", err)
	}
	if env.Error != nil {
		return env.Error
	}
	if string(env.Result) == "null" {
		return ErrNull
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Result, out)
}
