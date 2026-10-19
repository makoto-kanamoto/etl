package crypto

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
)

const testChain = "ETL-FR"

func hexDecode(s string) ([]byte, error) { return hex.DecodeString(s) }

func TestSignVerify(t *testing.T) {
	k, _ := GenerateKey()
	addr := k.PublicKey().Address("etl")
	msg := []byte("Marie envoie 5 ETL à Paul")
	sig, err := k.Sign(testChain, PurposeTx, msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(addr, testChain, PurposeTx, msg, sig); err != nil {
		t.Fatalf("signature valide rejetée : %v", err)
	}
}

func TestSignIsDeterministic(t *testing.T) {
	k, _ := GenerateKey()
	a, _ := k.Sign(testChain, PurposeTx, []byte("x"))
	b, _ := k.Sign(testChain, PurposeTx, []byte("x"))
	if a != b {
		t.Fatal("RFC 6979 : deux signatures du même message doivent être identiques")
	}
}

func TestVerifyRejects(t *testing.T) {
	k, _ := GenerateKey()
	other, _ := GenerateKey()
	addr := k.PublicKey().Address("etl")
	msg := []byte("5 ETL")
	sig, _ := k.Sign(testChain, PurposeTx, msg)

	tampered := sig
	tampered[40] ^= 0x01

	cases := []struct {
		name    string
		addr    Address
		chain   string
		purpose Purpose
		msg     []byte
		sig     Signature
	}{
		{"message modifié", addr, testChain, PurposeTx, []byte("6 ETL"), sig},
		{"rejeu sur un autre fork", addr, "ETL-DE", PurposeTx, msg, sig},
		{"autre usage (tx → vote)", addr, testChain, PurposeIPCVote, msg, sig},
		{"signature altérée", addr, testChain, PurposeTx, msg, tampered},
		{"autre signataire", other.PublicKey().Address("etl"), testChain, PurposeTx, msg, sig},
		{"signature nulle", addr, testChain, PurposeTx, msg, Signature{}},
	}
	for _, c := range cases {
		if err := Verify(c.addr, c.chain, c.purpose, c.msg, c.sig); err == nil {
			t.Errorf("%s : accepté à tort", c.name)
		}
	}
}

func TestSignRequiresChainAndPurpose(t *testing.T) {
	k, _ := GenerateKey()
	if _, err := k.Sign("", PurposeTx, nil); err != ErrEmptyChainID {
		t.Errorf("chain_id vide : err = %v", err)
	}
	if _, err := k.Sign(testChain, "", nil); err != ErrEmptyPurpose {
		t.Errorf("usage vide : err = %v", err)
	}
	if _, err := RecoverPublicKey("", PurposeTx, nil, Signature{}); err != ErrEmptyChainID {
		t.Errorf("recover chain_id vide : err = %v", err)
	}
}

func TestSignaturesAreLowS(t *testing.T) {
	for i := 0; i < 200; i++ {
		k, _ := GenerateKey()
		sig, _ := k.Sign(testChain, PurposeTx, []byte{byte(i)})
		var s secp256k1.ModNScalar
		s.SetByteSlice(sig[33:])
		if s.IsOverHalfOrder() {
			t.Fatalf("signature high-S produite : %x", sig)
		}
	}
}

// Une signature (r, s) a une jumelle mathématiquement valide (r, N-s).
// La bibliothèque l'accepte ; ETL doit la refuser pour qu'une transaction
// n'ait qu'une seule signature valide.
func TestHighSRejected(t *testing.T) {
	k, _ := GenerateKey()
	msg := []byte("malléabilité")
	sig, _ := k.Sign(testChain, PurposeTx, msg)

	var s secp256k1.ModNScalar
	s.SetByteSlice(sig[33:])
	s.Negate()
	high := sig
	sBytes := s.Bytes()
	copy(high[33:], sBytes[:])
	// Octet 0 = 27 + 4 (clé compressée) + code ; le bit 0 du code est la parité de y de R.
	// N-s correspond à -R : la parité s'inverse.
	high[0] = 31 + ((sig[0] - 31) ^ 0x01)

	h, _ := signingHash(testChain, PurposeTx, msg)
	if pub, _, err := ecdsa.RecoverCompact(high[:], h[:]); err != nil || !pub.IsEqual(k.PublicKey().k) {
		t.Fatalf("prémisse fausse : la jumelle high-S devrait être valide pour la bibliothèque (err=%v)", err)
	}
	if _, err := RecoverPublicKey(testChain, PurposeTx, msg, high); err != ErrInvalidSignature {
		t.Fatalf("signature high-S acceptée : err = %v", err)
	}
}

func TestUncompressedRecoveryFlagRejected(t *testing.T) {
	k, _ := GenerateKey()
	msg := []byte("x")
	sig, _ := k.Sign(testChain, PurposeTx, msg)
	sig[0] -= 4 // même (r, s), mais drapeau « clé non compressée »
	if _, err := RecoverPublicKey(testChain, PurposeTx, msg, sig); err != ErrInvalidSignature {
		t.Fatalf("drapeau non compressé accepté : err = %v", err)
	}
}

// Vecteur RFC 6979 secp256k1 largement repris (bitcoinjs, Trezor) :
// clé privée = 1, message = SHA256("Satoshi Nakamoto").
// Vérifie que la bibliothèque produit bien le nonce déterministe standard.
func TestRFC6979Vector(t *testing.T) {
	var one secp256k1.ModNScalar
	one.SetInt(1)
	priv := secp256k1.NewPrivateKey(&one)
	h := sha256.Sum256([]byte("Satoshi Nakamoto"))
	sig := ecdsa.SignCompact(priv, h[:], true)
	r, s := hex.EncodeToString(sig[1:33]), hex.EncodeToString(sig[33:])
	if r != "934b1ea10a4b3c1757e2b0c017d0b6143ce3c9a7e6a4a49860d7a6ab210ee3d8" ||
		s != "2442ce9d2b916064108014783e923ec36b49743e2ffa1c4496f01a512aafd9e5" {
		t.Fatalf("r = %s\ns = %s", r, s)
	}
}
