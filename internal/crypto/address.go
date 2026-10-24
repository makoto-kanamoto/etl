package crypto

import "errors"

const (
	// AddressHashSize est la taille du corps d'adresse : 20 premiers octets de Blake3(clé publique).
	AddressHashSize = 20
	// AddressVersion est la version du format d'adresse, encodée en premier symbole.
	// Réservée pour un futur type d'adresse ; seule la version 0 existe.
	AddressVersion = 0
)

var ErrInvalidAddress = errors.New("crypto: adresse invalide")

// Address est une adresse ETL : un préfixe réseau (HRP, ex. "etl") et une empreinte de clé publique.
// Forme texte : bech32m, ex. « etl1q… ».
type Address struct {
	HRP  string
	Hash [AddressHashSize]byte
}

// String renvoie l'adresse en bech32m minuscules, ou "" si le HRP est invalide.
func (a Address) String() string {
	data, _ := convertBits(a.Hash[:], 8, 5, true)
	s, err := bech32mEncode(a.HRP, append([]byte{AddressVersion}, data...))
	if err != nil {
		return ""
	}
	return s
}

// ParseAddress décode une adresse et vérifie qu'elle appartient au réseau hrp.
// Accepte tout-minuscules ou tout-majuscules (pratique pour les QR codes), jamais la casse mixte.
func ParseAddress(s, hrp string) (Address, error) {
	gotHRP, data, err := bech32mDecode(s)
	if err != nil || gotHRP != hrp || len(data) < 1 || data[0] != AddressVersion {
		return Address{}, ErrInvalidAddress
	}
	hash, err := convertBits(data[1:], 5, 8, false)
	if err != nil || len(hash) != AddressHashSize {
		return Address{}, ErrInvalidAddress
	}
	a := Address{HRP: hrp}
	copy(a.Hash[:], hash)
	return a, nil
}

// AddressHRP renvoie le préfixe de réseau d'une adresse valide (somme de contrôle comprise), quel que
// soit ce réseau : sert à signaler qu'une adresse appartient à un autre réseau.
func AddressHRP(s string) (string, bool) {
	hrp, _, err := bech32mDecode(s)
	if err != nil {
		return "", false
	}
	if _, err := ParseAddress(s, hrp); err != nil {
		return "", false
	}
	return hrp, true
}
