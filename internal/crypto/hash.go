// Package crypto regroupe les primitives cryptographiques d'ETL :
// hash Blake3, clés et signatures ECDSA secp256k1, adresses bech32m, preuve de travail.
package crypto

import (
	"encoding/hex"

	"github.com/zeebo/blake3"
)

// HashSize est la taille d'une empreinte en octets.
const HashSize = 32

// Hash est une empreinte Blake3-256.
type Hash [HashSize]byte

// Contextes Blake3 (mode derive_key) — un par usage.
// Une empreinte calculée pour un usage ne peut jamais être valide pour un autre.
// Ces chaînes font partie du protocole : les modifier casse la compatibilité.
const (
	ContextTx            = "etalon-etl 2026-10 transaction v1"
	ContextBlock         = "etalon-etl 2026-10 block v1"
	ContextAddress       = "etalon-etl 2026-10 address v1"
	ContextSignature     = "etalon-etl 2026-10 signature v1"
	ContextMerkle        = "etalon-etl 2026-10 merkle v1"
	ContextTicket        = "etalon-etl 2026-10 block ticket v1"
	ContextGenesis       = "etalon-etl 2026-10 genesis v1"
	ContextPoW           = "etalon-etl 2026-10 pow v1"
	ContextParticipation = "etalon-etl 2026-10 participation v1"
	ContextRegistry      = "etalon-etl 2026-10 registry v1"
	ContextState         = "etalon-etl 2026-10 state v1"
)

// Sum calcule l'empreinte Blake3-256 de data dans le contexte donné.
func Sum(context string, data []byte) Hash {
	var h Hash
	blake3.DeriveKey(context, data, h[:])
	return h
}

// String renvoie l'empreinte en hexadécimal.
func (h Hash) String() string {
	return hex.EncodeToString(h[:])
}
