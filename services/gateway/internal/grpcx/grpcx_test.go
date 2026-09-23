package grpcx

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// reply writes a gRPC-shaped unary response.
func reply(w http.ResponseWriter, msg []byte, status, gmsg string) {
	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
	if msg != nil {
		f := make([]byte, 5+len(msg))
		binary.BigEndian.PutUint32(f[1:5], uint32(len(msg)))
		copy(f[5:], msg)
		w.Write(f)
	}
	w.Header().Set("Grpc-Status", status)
	w.Header().Set("Grpc-Message", gmsg)
}

func echo(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	if r.Header.Get("Content-Type") != "application/grpc" || r.Header.Get("TE") != "trailers" || r.Proto != "HTTP/2.0" || len(b) < 5 {
		http.Error(w, "bad", 400)
		return
	}
	reply(w, append([]byte("echo:"), b[5:]...), "0", "")
}

func h2cServer(h http.Handler) *httptest.Server {
	s := httptest.NewUnstartedServer(h)
	var p http.Protocols
	p.SetUnencryptedHTTP2(true)
	s.Config.Protocols = &p
	s.Start()
	return s
}

func TestInvokeOverH2C(t *testing.T) {
	s := h2cServer(http.HandlerFunc(echo))
	defer s.Close()
	c, err := Dial("h2c://"+strings.TrimPrefix(s.URL, "http://"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := c.Invoke(ctx, "/svc/Echo", []byte("hi"))
	if err != nil || string(out) != "echo:hi" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestInvokeOverUnixSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "e.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var p http.Protocols
	p.SetUnencryptedHTTP2(true)
	srv := &http.Server{Handler: http.HandlerFunc(echo), Protocols: &p}
	go srv.Serve(ln)
	defer srv.Close()
	c, err := Dial("unix://"+sock, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.Invoke(context.Background(), "/svc/Echo", nil)
	if err != nil || string(out) != "echo:" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestStatusesAndProtocolErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/svc/Fail", func(w http.ResponseWriter, r *http.Request) {
		// trailers-only response, percent-encoded message
		w.Header().Set("Grpc-Status", "3")
		w.Header().Set("Grpc-Message", "bad%20field")
	})
	mux.HandleFunc("/svc/BadMsgEncoding", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Grpc-Status", "9")
		w.Header().Set("Grpc-Message", "100%")
	})
	mux.HandleFunc("/svc/NoStatus", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/svc/BadStatus", func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Grpc-Status", "x") })
	mux.HandleFunc("/svc/Http500", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", 500) })
	mux.HandleFunc("/svc/Compressed", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Trailer", "Grpc-Status")
		w.Write([]byte{1, 0, 0, 0, 0})
		w.Header().Set("Grpc-Status", "0")
	})
	mux.HandleFunc("/svc/Short", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Trailer", "Grpc-Status")
		w.Write([]byte{0, 0})
		w.Header().Set("Grpc-Status", "0")
	})
	mux.HandleFunc("/svc/LenMismatch", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Trailer", "Grpc-Status")
		w.Write([]byte{0, 0, 0, 0, 9, 1})
		w.Header().Set("Grpc-Status", "0")
	})
	mux.HandleFunc("/svc/Huge", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Trailer", "Grpc-Status")
		w.Write([]byte{0, 0xff, 0xff, 0xff, 0xff})
		w.Header().Set("Grpc-Status", "0")
	})
	s := h2cServer(mux)
	defer s.Close()
	c, _ := Dial("h2c://"+strings.TrimPrefix(s.URL, "http://"), nil)
	for method, want := range map[string]struct {
		code Code
		msg  string
	}{
		"/svc/Fail":           {InvalidArgument, "bad field"},
		"/svc/BadMsgEncoding": {FailedPrecondition, "100%"},
		"/svc/NoStatus":       {Unknown, "missing grpc-status"},
		"/svc/BadStatus":      {Unknown, "malformed grpc-status"},
		"/svc/Http500":        {Unavailable, "http 500"},
		"/svc/Compressed":     {Internal, "malformed or compressed response frame"},
		"/svc/Short":          {Internal, "malformed or compressed response frame"},
		"/svc/LenMismatch":    {Internal, "response frame length mismatch"},
		"/svc/Huge":           {Internal, "response frame length mismatch"},
	} {
		_, err := c.Invoke(context.Background(), method, nil)
		var st *Status
		if !errors.As(err, &st) || st.Code != want.code || st.Message != want.msg {
			t.Errorf("%s: got %v want %+v", method, err, want)
		}
	}
	var st *Status
	_, err := c.Invoke(context.Background(), "/svc/Fail", nil)
	if !errors.As(err, &st) || !strings.HasPrefix(st.Error(), "grpc 3:") {
		t.Fatalf("Error(): %v", err)
	}
}

func TestContextAndTransportFailures(t *testing.T) {
	slow := h2cServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	c, _ := Dial("h2c://"+strings.TrimPrefix(slow.URL, "http://"), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var st *Status
	if _, err := c.Invoke(ctx, "/x", nil); !errors.As(err, &st) || st.Code != DeadlineExceeded {
		t.Fatalf("deadline: %v", err)
	}
	// an already-expired deadline still sends a >=1ms grpc-timeout
	expired, cancel2 := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel2()
	if _, err := c.Invoke(expired, "/x", nil); !errors.As(err, &st) || st.Code != DeadlineExceeded {
		t.Fatalf("expired: %v", err)
	}
	cctx, ccancel := context.WithCancel(context.Background())
	ccancel()
	if _, err := c.Invoke(cctx, "/x", nil); !errors.As(err, &st) || st.Code != Canceled {
		t.Fatalf("cancel: %v", err)
	}
	// nothing listening
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	dead, _ := Dial("h2c://"+addr, nil)
	if _, err := dead.Invoke(context.Background(), "/x", nil); !errors.As(err, &st) || st.Code != Unavailable {
		t.Fatalf("dead: %v", err)
	}
	// invalid method path → request construction fails
	if _, err := c.Invoke(context.Background(), "/x\x7f", nil); !errors.As(err, &st) || st.Code != Internal {
		t.Fatalf("bad url: %v", err)
	}
}

func TestBodyReadError(t *testing.T) {
	s := h2cServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.Write([]byte("abc"))
		w.(http.Flusher).Flush()    // headers reach the client before the stream dies
		panic(http.ErrAbortHandler) // reset the stream mid-body
	}))
	defer s.Close()
	c, _ := Dial("h2c://"+strings.TrimPrefix(s.URL, "http://"), nil)
	var st *Status
	if _, err := c.Invoke(context.Background(), "/x", nil); !errors.As(err, &st) || st.Code != Unavailable {
		t.Fatalf("got %v", err)
	}
}

func TestDialValidation(t *testing.T) {
	for _, tc := range []struct {
		target string
		cfg    *tls.Config
	}{{"", nil}, {"unix://", nil}, {"127.0.0.1:1", nil}} {
		if _, err := Dial(tc.target, tc.cfg); err == nil {
			t.Errorf("Dial(%q) should fail", tc.target)
		}
	}
}

// ---- mTLS ------------------------------------------------------------------

type pki struct {
	pool        *x509.CertPool
	server, cli tls.Certificate
}

func mk(t *testing.T, tmpl, parent *x509.Certificate, pk, parentKey *ecdsa.PrivateKey) (*x509.Certificate, []byte) {
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &pk.PublicKey, parentKey)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return c, der
}

func newPKI(t *testing.T) pki {
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	ca, _ := mk(t, caTmpl, caTmpl, caKey, caKey)
	leaf := func(serial int64, usage x509.ExtKeyUsage) tls.Certificate {
		k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "leaf"}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{usage}}
		_, der := mk(t, tmpl, ca, k, caKey)
		return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: k}
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return pki{pool: pool, server: leaf(2, x509.ExtKeyUsageServerAuth), cli: leaf(3, x509.ExtKeyUsageClientAuth)}
}

func TestMutualTLS(t *testing.T) {
	p := newPKI(t)
	s := httptest.NewUnstartedServer(http.HandlerFunc(echo))
	s.EnableHTTP2 = true
	s.TLS = &tls.Config{Certificates: []tls.Certificate{p.server}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: p.pool, NextProtos: []string{"h2"}}
	s.StartTLS()
	defer s.Close()
	addr := strings.TrimPrefix(s.URL, "https://")

	good, err := Dial(addr, &tls.Config{RootCAs: p.pool, Certificates: []tls.Certificate{p.cli}, ServerName: "localhost", MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := good.Invoke(context.Background(), "/svc/Echo", []byte("x")); err != nil || string(out) != "echo:x" {
		t.Fatalf("authorised client: %q %v", out, err)
	}
	anon, _ := Dial(addr, &tls.Config{RootCAs: p.pool, ServerName: "localhost", MinVersion: tls.VersionTLS13})
	var st *Status
	if _, err := anon.Invoke(context.Background(), "/svc/Echo", nil); !errors.As(err, &st) || st.Code != Unavailable {
		t.Fatalf("client without a certificate must be refused: %v", err)
	}
}
