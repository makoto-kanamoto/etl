package crypto

import (
	"encoding/hex"
	"testing"
)

// Vecteurs officiels BLAKE3, mode derive_key (32 premiers octets) :
// https://github.com/BLAKE3-team/BLAKE3/blob/master/test_vectors/test_vectors.json
// Entrée : octets i % 251 pour i de 0 à input_len-1.
func TestSumOfficialVectors(t *testing.T) {
	const ctx = "BLAKE3 2019-12-27 16:29:52 test vectors context"
	cases := []struct {
		inputLen int
		want     string
	}{
		{0, "2cc39783c223154fea8dfb7c1b1660f2ac2dcbd1c1de8277b0b0dd39b7e50d7d"},
		{1, "b3e2e340a117a499c6cf2398a19ee0d29cca2bb7404c73063382693bf66cb06c"},
		{1023, "74a16c1c3d44368a86e1ca6df64be6a2f64cce8f09220787450722d85725dea5"},
		{1024, "7356cd7720d5b66b6d0697eb3177d9f8d73a4a5c5e968896eb6a689684302706"},
		{1025, "effaa245f065fbf82ac186839a249707c3bddf6d3fdda22d1b95a3c970379bcb"},
	}
	for _, c := range cases {
		in := make([]byte, c.inputLen)
		for i := range in {
			in[i] = byte(i % 251)
		}
		if got := Sum(ctx, in).String(); got != c.want {
			t.Errorf("Sum(len=%d) = %s, attendu %s", c.inputLen, got, c.want)
		}
	}
}

func TestSumContextsAreSeparated(t *testing.T) {
	data := []byte("même donnée")
	seen := map[Hash]string{}
	for _, ctx := range []string{ContextTx, ContextBlock, ContextAddress, ContextSignature, ContextMerkle, ContextTicket, ContextGenesis, ContextPoW, ContextParticipation, ContextRegistry, ContextState, ContextMessage} {
		h := Sum(ctx, data)
		if prev, ok := seen[h]; ok {
			t.Fatalf("contextes %q et %q donnent la même empreinte", prev, ctx)
		}
		seen[h] = ctx
	}
}

func TestHashString(t *testing.T) {
	var h Hash
	h[0], h[31] = 0xab, 0x01
	s := h.String()
	if len(s) != 64 || s[:2] != "ab" || s[62:] != "01" {
		t.Errorf("String() = %s", s)
	}
	if _, err := hex.DecodeString(s); err != nil {
		t.Error(err)
	}
}
