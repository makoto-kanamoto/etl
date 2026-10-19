package crypto

// Encodage bech32m (BIP-350), implémenté ici plutôt qu'importé :
// moins de 150 lignes, auditables, couvertes par les vecteurs officiels du BIP.

import (
	"errors"
	"strings"
)

const (
	bech32Charset   = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
	bech32mConst    = 0x2bc830a3
	bech32MaxLength = 90
	bech32ChecksumN = 6
)

var errBech32 = errors.New("crypto: chaîne bech32m invalide")

func bech32Polymod(values []byte) uint32 {
	gen := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := 0; i < 5; i++ {
			if (top>>i)&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	return chk
}

func bech32HRPExpand(hrp string) []byte {
	out := make([]byte, 0, len(hrp)*2+1)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]>>5)
	}
	out = append(out, 0)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]&31)
	}
	return out
}

func validHRP(hrp string) bool {
	if len(hrp) < 1 || len(hrp) > 83 {
		return false
	}
	for i := 0; i < len(hrp); i++ {
		if hrp[i] < 33 || hrp[i] > 126 {
			return false
		}
	}
	return true
}

// bech32mEncode encode des valeurs 5 bits. hrp doit être en minuscules.
func bech32mEncode(hrp string, data []byte) (string, error) {
	if !validHRP(hrp) || hrp != strings.ToLower(hrp) {
		return "", errBech32
	}
	if len(hrp)+1+len(data)+bech32ChecksumN > bech32MaxLength {
		return "", errBech32
	}
	values := append(bech32HRPExpand(hrp), data...)
	values = append(values, make([]byte, bech32ChecksumN)...)
	mod := bech32Polymod(values) ^ bech32mConst

	var sb strings.Builder
	sb.WriteString(hrp)
	sb.WriteByte('1')
	for _, d := range data {
		sb.WriteByte(bech32Charset[d])
	}
	for i := 0; i < bech32ChecksumN; i++ {
		sb.WriteByte(bech32Charset[(mod>>(5*(5-i)))&31])
	}
	return sb.String(), nil
}

// bech32mDecode décode une chaîne bech32m. Renvoie le hrp en minuscules et les valeurs 5 bits.
// Rejette la casse mixte, les caractères hors alphabet et les checksums bech32 (non m).
func bech32mDecode(s string) (string, []byte, error) {
	if len(s) > bech32MaxLength {
		return "", nil, errBech32
	}
	lower, upper := strings.ToLower(s), strings.ToUpper(s)
	if s != lower && s != upper {
		return "", nil, errBech32
	}
	s = lower
	pos := strings.LastIndexByte(s, '1')
	if pos < 1 || pos+1+bech32ChecksumN > len(s) {
		return "", nil, errBech32
	}
	hrp := s[:pos]
	if !validHRP(hrp) {
		return "", nil, errBech32
	}
	data := make([]byte, 0, len(s)-pos-1)
	for i := pos + 1; i < len(s); i++ {
		v := strings.IndexByte(bech32Charset, s[i])
		if v < 0 {
			return "", nil, errBech32
		}
		data = append(data, byte(v))
	}
	if bech32Polymod(append(bech32HRPExpand(hrp), data...)) != bech32mConst {
		return "", nil, errBech32
	}
	return hrp, data[:len(data)-bech32ChecksumN], nil
}

// convertBits regroupe des valeurs de fromBits bits en valeurs de toBits bits.
// Sans pad (décodage), refuse un reliquat ≥ fromBits bits ou non nul.
func convertBits(data []byte, fromBits, toBits uint, pad bool) ([]byte, error) {
	var acc, bits uint
	maxv := uint(1)<<toBits - 1
	out := make([]byte, 0, len(data)*int(fromBits)/int(toBits)+1)
	for _, v := range data {
		if uint(v)>>fromBits != 0 {
			return nil, errBech32
		}
		acc = acc<<fromBits | uint(v)
		bits += fromBits
		for bits >= toBits {
			bits -= toBits
			out = append(out, byte(acc>>bits&maxv))
		}
	}
	if pad {
		if bits > 0 {
			out = append(out, byte(acc<<(toBits-bits)&maxv))
		}
	} else if bits >= fromBits || acc<<(toBits-bits)&maxv != 0 {
		return nil, errBech32
	}
	return out, nil
}
