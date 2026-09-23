package keccak

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type vectors struct {
	Keccak []struct {
		InputHex string `json:"input_hex"`
		Digest   string `json:"digest"`
	} `json:"keccak256"`
	EIP55 []string `json:"eip55"`
}

func load(t *testing.T) vectors {
	t.Helper()
	b, err := os.ReadFile("../../../../contracts/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSum256GoldenVectors(t *testing.T) {
	for _, c := range load(t).Keccak {
		in, _ := hex.DecodeString(c.InputHex)
		if got := Sum256(in); hex.EncodeToString(got[:]) != c.Digest {
			t.Errorf("%s: got %x", c.InputHex, got)
		}
	}
}

func TestSum256MultiBlock(t *testing.T) {
	// Exactly one rate-block and beyond must not panic and must differ.
	a := Sum256([]byte(strings.Repeat("a", rate)))
	b := Sum256([]byte(strings.Repeat("a", rate+1)))
	if a == b {
		t.Fatal("collision")
	}
}

func TestEIP55RoundTrip(t *testing.T) {
	for _, s := range load(t).EIP55 {
		a, err := ParseAddress(s)
		if err != nil || Checksum(a) != s {
			t.Errorf("%s: %v %s", s, err, Checksum(a))
		}
		if _, err := ParseAddress(strings.ToLower(s)); err != nil {
			t.Errorf("lower %s rejected", s)
		}
	}
}

func TestParseAddressRejects(t *testing.T) {
	good := load(t).EIP55[0]
	bad := []string{"", "0x12", "5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed00", "0xZZAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
		good[:41] + strings.ToUpper(good[41:]), // flips checksum case of last char
	}
	// ensure the last one really differs
	if bad[4] == good {
		bad[4] = "0x5AAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"
	}
	for _, s := range bad {
		if _, err := ParseAddress(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}
