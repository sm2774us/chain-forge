// Package jsonrpc implements JSON-RPC 2.0 framing (single + batch) shared by
// the gateway and the simulated chain node.
package jsonrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Standard error codes (JSON-RPC 2.0 §5.1) plus gateway-specific ones.
const (
	CodeParse          = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603
	CodeRateLimited    = -32005
	CodeUpstream       = -32002
)

// MaxBatch bounds batch amplification (a classic RPC DoS vector).
const MaxBatch = 100

// Request is a JSON-RPC request object.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      json.RawMessage `json:"id"`
}

// Error is a JSON-RPC error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("jsonrpc %d: %s", e.Code, e.Message) }

// Response is a JSON-RPC response object.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
	ID      json.RawMessage `json:"id"`
}

// Handler serves one request.
type Handler func(ctx context.Context, req Request) Response

// OK builds a success response.
func OK(id json.RawMessage, v any) Response {
	b, err := json.Marshal(v)
	if err != nil {
		return Fail(id, CodeInternal, err.Error())
	}
	return Response{JSONRPC: "2.0", Result: b, ID: normID(id)}
}

// Fail builds an error response.
func Fail(id json.RawMessage, code int, msg string) Response {
	return Response{JSONRPC: "2.0", Error: &Error{Code: code, Message: msg}, ID: normID(id)}
}

func normID(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

// Decode parses a body into requests. batch reports whether it was an array.
func Decode(body []byte) (reqs []Request, batch bool, err *Error) {
	t := bytes.TrimSpace(body)
	if len(t) == 0 {
		return nil, false, &Error{CodeInvalidRequest, "empty body"}
	}
	if t[0] == '[' {
		if e := json.Unmarshal(t, &reqs); e != nil {
			return nil, true, &Error{CodeParse, "parse error"}
		}
		if len(reqs) == 0 {
			return nil, true, &Error{CodeInvalidRequest, "empty batch"}
		}
		if len(reqs) > MaxBatch {
			return nil, true, &Error{CodeInvalidRequest, "batch too large"}
		}
		return reqs, true, nil
	}
	var r Request
	if e := json.Unmarshal(t, &r); e != nil {
		return nil, false, &Error{CodeParse, "parse error"}
	}
	return []Request{r}, false, nil
}

// Serve decodes an HTTP request body, dispatches to h and writes the reply.
func Serve(w http.ResponseWriter, r *http.Request, h Handler) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		_ = json.NewEncoder(w).Encode(Fail(nil, CodeInvalidRequest, "body too large"))
		return
	}
	reqs, batch, derr := Decode(body)
	if derr != nil {
		_ = json.NewEncoder(w).Encode(Fail(nil, derr.Code, derr.Message))
		return
	}
	out := make([]Response, len(reqs))
	for i, q := range reqs {
		if q.JSONRPC != "2.0" || q.Method == "" {
			out[i] = Fail(q.ID, CodeInvalidRequest, "invalid request")
			continue
		}
		out[i] = h(r.Context(), q)
	}
	if batch {
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	_ = json.NewEncoder(w).Encode(out[0])
}
