package blockchain

import (
	"errors"
	"math"
	"math/bits"
	"strconv"
	"strings"
)

// UnitsPerETL : 1 ETL = 10^8 unités. Tous les calculs se font en unités (uint64).
const (
	UnitsPerETL   uint64 = 100_000_000
	unitsDecimals        = 8
	maxBps        uint64 = 10_000
)

var (
	ErrInvalidAmount = errors.New("blockchain: montant invalide")
	ErrOverflow      = errors.New("blockchain: dépassement de capacité")
	ErrInvalidBps    = errors.New("blockchain: points de base invalides")
)

// ParseETL convertit un montant décimal (« 10 », « 0.5 », « 0.00000001 ») en unités.
// Aucun float : la conversion est faite chiffre par chiffre.
// Refuse le signe, l'exposant, plus de 8 décimales et les dépassements.
func ParseETL(s string) (uint64, error) {
	intPart, frac, hasDot := strings.Cut(s, ".")
	if intPart == "" || (hasDot && frac == "") || len(frac) > unitsDecimals {
		return 0, ErrInvalidAmount
	}
	whole, err := parseDigits(intPart)
	if err != nil {
		return 0, err
	}
	fracUnits, err := parseDigits(frac + strings.Repeat("0", unitsDecimals-len(frac)))
	if err != nil {
		return 0, err
	}
	hi, lo := bits.Mul64(whole, UnitsPerETL)
	if hi != 0 {
		return 0, ErrOverflow
	}
	return checkedAdd(lo, fracUnits)
}

func parseDigits(s string) (uint64, error) {
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, ErrInvalidAmount
		}
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, ErrOverflow
	}
	return v, nil
}

// UnitsFromConfig convertit une valeur décimale lue dans default.toml (float64) en unités.
// Passe par la représentation décimale la plus courte du float (10.0 → "10", 0.1 → "0.1"),
// jamais par une multiplication flottante.
func UnitsFromConfig(f float64) (uint64, error) {
	if f < 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, ErrInvalidAmount
	}
	return ParseETL(strconv.FormatFloat(f, 'f', -1, 64))
}

// FormatETL affiche un montant en ETL, sans zéros inutiles (« 10 », « 0.005 »).
func FormatETL(units uint64) string {
	whole, frac := units/UnitsPerETL, units%UnitsPerETL
	if frac == 0 {
		return strconv.FormatUint(whole, 10)
	}
	f := strings.TrimRight(strconv.FormatUint(frac+UnitsPerETL, 10)[1:], "0")
	return strconv.FormatUint(whole, 10) + "." + f
}

// Fee calcule les frais : amount × bps / 10000, arrondi à l'unité inférieure.
// Un montant de moins de 1000 unités paie donc 0 frais (choix assumé).
func Fee(amount, bps uint64) (uint64, error) {
	if bps > maxBps {
		return 0, ErrInvalidBps
	}
	hi, lo := bits.Mul64(amount, bps)
	q, _ := bits.Div64(hi, lo, maxBps) // hi < maxBps car bps ≤ maxBps : pas de panique
	return q, nil
}

// RequiredFee renvoie les frais exigés d'une transaction : max(amount × FeeBps / 10000, MinFee).
// Le minimum rend le spam coûteux ; un paiement de 0.1 ETL ou plus n'est pas concerné.
func RequiredFee(amount uint64, p Params) (uint64, error) {
	fee, err := Fee(amount, p.FeeBps)
	if err != nil {
		return 0, err
	}
	return max(fee, p.MinFee), nil
}

func checkedAdd(a, b uint64) (uint64, error) {
	s, carry := bits.Add64(a, b, 0)
	if carry != 0 {
		return 0, ErrOverflow
	}
	return s, nil
}
