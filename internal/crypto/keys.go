package crypto

import (
	"errors"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

const (
	// PrivateKeySize est la taille d'une clé privée en octets.
	PrivateKeySize = 32
	// PublicKeySize est la taille d'une clé publique compressée en octets.
	PublicKeySize = 33
)

var (
	ErrInvalidPrivateKey = errors.New("crypto: clé privée invalide")
	ErrInvalidPublicKey  = errors.New("crypto: clé publique invalide")
	errNoMarshal         = errors.New("crypto: une clé privée ne se sérialise pas")
)

// PrivateKey est une clé privée secp256k1.
// Elle ne s'affiche ni ne se sérialise par accident : fmt affiche « PrivateKey(***) »
// et l'encodage texte/JSON échoue. Seul Bytes() expose la clé.
type PrivateKey struct {
	k *secp256k1.PrivateKey
}

// GenerateKey crée une clé privée à partir de l'aléa du système (crypto/rand).
func GenerateKey() (*PrivateKey, error) {
	k, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		return nil, err
	}
	return &PrivateKey{k: k}, nil
}

// PrivateKeyFromBytes charge une clé privée de 32 octets.
// Rejette 0 et toute valeur ≥ N (ordre de la courbe) au lieu de la réduire en silence.
func PrivateKeyFromBytes(b []byte) (*PrivateKey, error) {
	if len(b) != PrivateKeySize {
		return nil, ErrInvalidPrivateKey
	}
	var s secp256k1.ModNScalar
	if overflow := s.SetByteSlice(b); overflow || s.IsZero() {
		return nil, ErrInvalidPrivateKey
	}
	return &PrivateKey{k: secp256k1.NewPrivateKey(&s)}, nil
}

// Bytes renvoie les 32 octets de la clé privée.
func (p *PrivateKey) Bytes() []byte {
	return p.k.Serialize()
}

// PublicKey renvoie la clé publique associée.
func (p *PrivateKey) PublicKey() PublicKey {
	return PublicKey{k: p.k.PubKey()}
}

// Zero efface la clé de la mémoire. La clé est inutilisable ensuite.
func (p *PrivateKey) Zero() {
	p.k.Zero()
}

func (p *PrivateKey) String() string               { return "PrivateKey(***)" }
func (p *PrivateKey) GoString() string             { return "PrivateKey(***)" }
func (p *PrivateKey) MarshalText() ([]byte, error) { return nil, errNoMarshal }

// PublicKey est une clé publique secp256k1.
type PublicKey struct {
	k *secp256k1.PublicKey
}

// ParsePublicKey charge une clé publique compressée de 33 octets.
// La forme non compressée est refusée : une seule représentation par clé.
func ParsePublicKey(b []byte) (PublicKey, error) {
	if len(b) != PublicKeySize {
		return PublicKey{}, ErrInvalidPublicKey
	}
	k, err := secp256k1.ParsePubKey(b)
	if err != nil {
		return PublicKey{}, ErrInvalidPublicKey
	}
	return PublicKey{k: k}, nil
}

// Bytes renvoie la clé publique compressée (33 octets).
func (p PublicKey) Bytes() []byte {
	return p.k.SerializeCompressed()
}

// Address renvoie l'adresse ETL de la clé publique pour le préfixe réseau hrp (ex. "etl").
func (p PublicKey) Address(hrp string) Address {
	sum := Sum(ContextAddress, p.Bytes())
	a := Address{HRP: hrp}
	copy(a.Hash[:], sum[:AddressHashSize])
	return a
}
