// Package grpcx is a minimal unary gRPC client on the standard library alone.
//
// gRPC is HTTP/2 + length-prefixed protobuf + trailers; Go 1.24's net/http
// speaks all three (including unencrypted HTTP/2 for Unix sockets), so no
// third-party module sits in the request path. Three transports are supported:
//
//	unix:///run/engine.sock   Unix domain socket (no network exposure at all)
//	host:port                 mTLS over TCP (requires a *tls.Config)
//	h2c://host:port           plaintext HTTP/2, local development only
package grpcx

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Code is a gRPC status code.
type Code int

// The codes this gateway distinguishes.
const (
	OK                 Code = 0
	Canceled           Code = 1
	Unknown            Code = 2
	InvalidArgument    Code = 3
	DeadlineExceeded   Code = 4
	ResourceExhausted  Code = 8
	FailedPrecondition Code = 9
	Unimplemented      Code = 12
	Internal           Code = 13
	Unavailable        Code = 14
)

// MaxMessage bounds any single response frame.
const MaxMessage = 4 << 20

// Status is a non-OK gRPC result.
type Status struct {
	Code    Code
	Message string
}

func (s *Status) Error() string { return fmt.Sprintf("grpc %d: %s", s.Code, s.Message) }

// Client is a connection-pooled unary client.
type Client struct {
	hc   *http.Client
	base string
}

func h2c() *http.Protocols {
	var p http.Protocols
	p.SetUnencryptedHTTP2(true)
	return &p
}

// Dial prepares a client; connections are established lazily and reused.
func Dial(target string, tlsCfg *tls.Config) (*Client, error) {
	tr := &http.Transport{MaxIdleConnsPerHost: 64, IdleConnTimeout: 2 * time.Minute}
	c := &Client{hc: &http.Client{Transport: tr}}
	switch {
	case strings.HasPrefix(target, "unix://"):
		path := strings.TrimPrefix(target, "unix://")
		if path == "" {
			return nil, errors.New("grpcx: empty unix socket path")
		}
		tr.Protocols = h2c()
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		}
		c.base = "http://engine.local"
	case strings.HasPrefix(target, "h2c://"):
		tr.Protocols = h2c()
		c.base = "http://" + strings.TrimPrefix(target, "h2c://")
	case target == "":
		return nil, errors.New("grpcx: empty target")
	default:
		if tlsCfg == nil {
			return nil, errors.New("grpcx: TCP targets require mTLS (or use h2c:// for local development)")
		}
		tr.TLSClientConfig = tlsCfg
		tr.ForceAttemptHTTP2 = true
		c.base = "https://" + target
	}
	return c, nil
}

// Close releases idle connections.
func (c *Client) Close() { c.hc.CloseIdleConnections() }

func ctxStatus(err error) *Status {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &Status{DeadlineExceeded, "deadline exceeded"}
	case errors.Is(err, context.Canceled):
		return &Status{Canceled, "canceled"}
	}
	return &Status{Unavailable, err.Error()}
}

// Invoke performs one unary call with an already-encoded protobuf request.
func (c *Client) Invoke(ctx context.Context, method string, req []byte) ([]byte, error) {
	frame := make([]byte, 5+len(req))
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(req)))
	copy(frame[5:], req)
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+method, bytes.NewReader(frame))
	if err != nil {
		return nil, &Status{Internal, err.Error()}
	}
	hr.Header.Set("Content-Type", "application/grpc")
	hr.Header.Set("TE", "trailers")
	hr.Header.Set("User-Agent", "chainforge-gateway")
	if dl, ok := ctx.Deadline(); ok {
		ms := time.Until(dl).Milliseconds()
		if ms < 1 {
			ms = 1
		}
		hr.Header.Set("Grpc-Timeout", strconv.FormatInt(ms, 10)+"m")
	}
	resp, err := c.hc.Do(hr)
	if err != nil {
		return nil, ctxStatus(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxMessage+5+1))
	if err != nil {
		return nil, ctxStatus(err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &Status{Unavailable, "http " + strconv.Itoa(resp.StatusCode)}
	}
	// Trailers-only responses carry the status in the headers.
	raw := resp.Trailer.Get("Grpc-Status")
	msg := resp.Trailer.Get("Grpc-Message")
	if raw == "" {
		raw, msg = resp.Header.Get("Grpc-Status"), resp.Header.Get("Grpc-Message")
	}
	if raw == "" {
		return nil, &Status{Unknown, "missing grpc-status"}
	}
	code, err := strconv.Atoi(raw)
	if err != nil {
		return nil, &Status{Unknown, "malformed grpc-status"}
	}
	if code != 0 {
		if dec, err := url.PathUnescape(msg); err == nil {
			msg = dec
		}
		return nil, &Status{Code(code), msg}
	}
	if len(body) < 5 || body[0] != 0 {
		return nil, &Status{Internal, "malformed or compressed response frame"}
	}
	n := binary.BigEndian.Uint32(body[1:5])
	if n > MaxMessage || int(n) != len(body)-5 {
		return nil, &Status{Internal, "response frame length mismatch"}
	}
	return body[5:], nil
}
