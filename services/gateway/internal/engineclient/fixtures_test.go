package engineclient

import (
	"bytes"
	"math/big"

	"github.com/chainforge/gateway/internal/pbwire"
)

func rep(b byte, n int) []byte { return bytes.Repeat([]byte{b}, n) }

func addr20(b byte) Address { var a Address; copy(a[:], rep(b, 20)); return a }

func eth(n int64) *big.Int { return new(big.Int).Mul(big.NewInt(n), big.NewInt(1e18)) }

// goldenRequest / goldenResponse are the shared wire fixtures (contracts/wire_golden.json).
func goldenRequest() SimRequest {
	to := addr20(0x22)
	var key [32]byte
	key[31] = 1
	return SimRequest{
		ChainID: 1337, From: addr20(0x11), To: &to, Value: big.NewInt(1000), Data: []byte{0xde, 0xad, 0xbe, 0xef},
		GasLimit: 50000, GasPrice: big.NewInt(1_000_000_000),
		State:       []Account{{Address: addr20(0x11), Balance: eth(1), Nonce: 5, Code: []byte{0x60, 0x01}, Storage: []Slot{{Key: key, Value: big.NewInt(0x2a)}}}},
		BlockNumber: 19_000_000, Timestamp: 1_700_000_000,
	}
}

func goldenResponseBytes() []byte {
	var log pbwire.Buf
	log.Raw(1, rep(0x22, 20))
	log.RawAlways(2, rep(0xaa, 32))
	log.Raw(3, []byte{1})
	before := make([]byte, 32)
	eth(1).FillBytes(before)
	after := make([]byte, 32)
	new(big.Int).Sub(eth(1), big.NewInt(1000)).FillBytes(after)
	var bc pbwire.Buf
	bc.Raw(1, rep(0x11, 20))
	bc.Raw(2, before)
	bc.Raw(3, after)
	var w pbwire.Buf
	w.Uint(1, 1)
	w.Uint(2, 21000)
	w.Raw(3, []byte{0x2a})
	w.Raw(4, []byte("ok"))
	w.RawAlways(5, log.Bytes())
	w.RawAlways(6, bc.Bytes())
	w.Uint(7, 42)
	w.Raw(8, rep(0x33, 20))
	return w.Bytes()
}
