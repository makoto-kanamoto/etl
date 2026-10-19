package crypto

import (
	"encoding/hex"
	"testing"
)

func hexString(b []byte) string { return hex.EncodeToString(b) }

func TestMeetsTarget(t *testing.T) {
	var target Hash
	target[1] = 0x10 // 0x0010…00

	below, equal, above := target, target, target
	below[1] = 0x0f
	above[31] = 0x01

	if !below.MeetsTarget(target) {
		t.Error("hash sous la cible refusé")
	}
	if !equal.MeetsTarget(target) {
		t.Error("hash égal à la cible refusé")
	}
	if above.MeetsTarget(target) {
		t.Error("hash au-dessus de la cible accepté")
	}
}

func TestCmpLowestWins(t *testing.T) {
	var a, b Hash
	a[0], b[0] = 0x00, 0x01
	b[31] = 0x00
	a[31] = 0xff // les octets de poids fort décident
	if a.Cmp(b) != -1 || b.Cmp(a) != 1 || a.Cmp(a) != 0 {
		t.Error("ordre big-endian incorrect")
	}
}
