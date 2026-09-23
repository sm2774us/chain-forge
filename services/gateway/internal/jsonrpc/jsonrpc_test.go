package jsonrpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func echo(_ context.Context, q Request) Response { return OK(q.ID, q.Method) }

func do(body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	Serve(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), echo)
	return rec
}

func TestSingle(t *testing.T) {
	var r Response
	_ = json.Unmarshal(do(`{"jsonrpc":"2.0","method":"m","id":1}`).Body.Bytes(), &r)
	if string(r.Result) != `"m"` || string(r.ID) != "1" {
		t.Fatalf("bad %+v", r)
	}
}

func TestBatch(t *testing.T) {
	var rs []Response
	_ = json.Unmarshal(do(`[{"jsonrpc":"2.0","method":"a","id":1},{"jsonrpc":"1.0","method":"b","id":2}]`).Body.Bytes(), &rs)
	if len(rs) != 2 || rs[0].Error != nil || rs[1].Error == nil {
		t.Fatalf("bad %+v", rs)
	}
}

func TestDecodeErrors(t *testing.T) {
	big := "[" + strings.Repeat(`{"jsonrpc":"2.0","method":"m","id":1},`, MaxBatch) + `{"jsonrpc":"2.0","method":"m","id":1}]`
	cases := map[string]int{"": CodeInvalidRequest, "[": CodeParse, "{": CodeParse, "[]": CodeInvalidRequest, big: CodeInvalidRequest}
	for body, want := range cases {
		var r Response
		_ = json.Unmarshal(do(body).Body.Bytes(), &r)
		if r.Error == nil || r.Error.Code != want {
			t.Errorf("%.20q: got %+v want %d", body, r.Error, want)
		}
	}
}

func TestBodyTooLarge(t *testing.T) {
	var r Response
	_ = json.Unmarshal(do(strings.Repeat("x", 2<<20)).Body.Bytes(), &r)
	if r.Error == nil || r.Error.Code != CodeInvalidRequest {
		t.Fatal("want too-large error")
	}
}

func TestHelpers(t *testing.T) {
	if !strings.Contains((&Error{1, "x"}).Error(), "x") {
		t.Fatal("Error()")
	}
	if r := OK(nil, make(chan int)); r.Error == nil || string(r.ID) != "null" {
		t.Fatal("marshal failure must yield error + null id")
	}
}
