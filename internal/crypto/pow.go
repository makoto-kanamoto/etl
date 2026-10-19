package crypto

import "bytes"

// Les empreintes de preuve de travail sont lues comme des entiers 256 bits big-endian.
// Le calcul de la cible (ajustement de difficulté) appartient à internal/blockchain.

// MeetsTarget indique si h est une preuve de travail valide pour target (h ≤ target).
func (h Hash) MeetsTarget(target Hash) bool {
	return bytes.Compare(h[:], target[:]) <= 0
}

// Cmp compare deux empreintes : -1 si h < o, 0 si égales, +1 si h > o.
// Entre plusieurs blocs valides du même jour, le hash le plus bas gagne.
func (h Hash) Cmp(o Hash) int {
	return bytes.Compare(h[:], o[:])
}
