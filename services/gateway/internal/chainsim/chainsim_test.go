package chainsim

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chainforge/gateway/internal/jsonrpc"
	"github.com/chainforge/gateway/internal/keccak"
)

func call(n *Node, method, params string) jsonrpc.Response {
	return n.Handle(context.Background(), jsonrpc.Request{JSONRPC: "2.0", Method: method, Params: json.RawMessage(params), ID: json.RawMessage("1")})
}

func str(t *testing.T, r jsonrpc.Response) string {
	t.Helper()
	var s string
	if r.Error != nil || json.Unmarshal(r.Result, &s) != nil {
		t.Fatalf("unexpected: %+v", r)
	}
	return s
}

func TestScalarMethods(t *testing.T) {
	n := New(1337)
	n.Mine()
	if str(t, call(n, "eth_chainId", "[]")) != "0x539" || str(t, call(n, "eth_blockNumber", "[]")) != "0x1" {
		t.Fatal("chainId/blockNumber")
	}
	if str(t, call(n, "eth_gasPrice", "[]")) != "0x4e3b29200" {
		t.Fatal("gasPrice")
	}
	addr := `["0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed","latest"]`
	if str(t, call(n, "eth_getBalance", addr)) != str(t, call(n, "eth_getBalance", addr)) {
		t.Fatal("balance must be deterministic")
	}
}

func TestBlocks(t *testing.T) {
	n := New(1)
	n.Mine()
	n.Mine()
	var b map[string]any
	_ = json.Unmarshal(call(n, "eth_getBlockByNumber", `["latest",false]`).Result, &b)
	if b["number"] != "0x2" {
		t.Fatalf("latest: %v", b)
	}
	hash := b["hash"].(string)
	var byHash map[string]any
	_ = json.Unmarshal(call(n, "eth_getBlockByHash", `["`+hash+`",false]`).Result, &byHash)
	if byHash["hash"] != hash {
		t.Fatal("by hash")
	}
	for _, tag := range []string{"earliest", "0x1", "finalized"} {
		if r := call(n, "eth_getBlockByNumber", `["`+tag+`",false]`); string(r.Result) == "null" {
			t.Errorf("%s not found", tag)
		}
	}
	for _, tag := range []string{"0x99", "zz", "0xzz", "7"} {
		if r := call(n, "eth_getBlockByNumber", `["`+tag+`",false]`); string(r.Result) != "null" {
			t.Errorf("%s should be null", tag)
		}
	}
	if r := call(n, "eth_getBlockByHash", `["0xdead",false]`); string(r.Result) != "null" {
		t.Fatal("unknown hash must be null")
	}
}

func TestReorg(t *testing.T) {
	n := New(1)
	for i := 0; i < 5; i++ {
		n.Mine()
	}
	var before, after map[string]any
	_ = json.Unmarshal(call(n, "eth_getBlockByNumber", `["0x4",false]`).Result, &before)
	n.Reorg(2)
	_ = json.Unmarshal(call(n, "eth_getBlockByNumber", `["0x4",false]`).Result, &after)
	if before["hash"] == after["hash"] || str(t, call(n, "eth_blockNumber", "[]")) != "0x6" {
		t.Fatal("reorg must replace hashes and lengthen the chain")
	}
	n.Reorg(1000) // clamps to genesis-1
	if str(t, call(n, "eth_blockNumber", "[]")) != "0x7" {
		t.Fatal("clamp")
	}
}

func TestAccountAndSubmitMethods(t *testing.T) {
	n := New(1)
	addr := `["0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed","latest"]`
	if str(t, call(n, "eth_getTransactionCount", addr)) != "0x0" || str(t, call(n, "eth_getCode", addr)) != "0x" {
		t.Fatal("account state")
	}
	want := keccak.Sum256([]byte{1, 2})
	if got := str(t, call(n, "eth_sendRawTransaction", `["0x0102"]`)); got != "0x"+hex.EncodeToString(want[:]) {
		t.Fatalf("tx hash %s", got)
	}
	for _, c := range [][2]string{
		{"eth_getTransactionCount", "[]"}, {"eth_getTransactionCount", `["nope"]`},
		{"eth_getCode", "[]"}, {"eth_getCode", `["nope"]`},
		{"eth_sendRawTransaction", "[]"}, {"eth_sendRawTransaction", `["0x"]`}, {"eth_sendRawTransaction", `["0102"]`}, {"eth_sendRawTransaction", `["0xzz"]`},
	} {
		if r := call(n, c[0], c[1]); r.Error == nil || r.Error.Code != jsonrpc.CodeInvalidParams {
			t.Errorf("%v: %+v", c, r)
		}
	}
}

func TestErrors(t *testing.T) {
	n := New(1)
	for _, c := range [][2]string{{"eth_getBalance", "[]"}, {"eth_getBalance", `["nope","latest"]`}, {"eth_getBlockByNumber", "{}"}, {"eth_getBlockByHash", "[]"}} {
		if r := call(n, c[0], c[1]); r.Error == nil || r.Error.Code != jsonrpc.CodeInvalidParams {
			t.Errorf("%v: %+v", c, r)
		}
	}
	if r := call(n, "eth_nope", "[]"); r.Error == nil || r.Error.Code != jsonrpc.CodeMethodNotFound {
		t.Fatal("method not found")
	}
	if _, ok := params(json.RawMessage(`[false]`), 1); !ok {
		t.Fatal("non-string param retained")
	}
}

func TestServeHTTP(t *testing.T) {
	rec := httptest.NewRecorder()
	New(1).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0","method":"eth_chainId","id":1}`)))
	if !strings.Contains(rec.Body.String(), "0x1") {
		t.Fatal(rec.Body.String())
	}
}
