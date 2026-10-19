package blockchain

import (
	"math"
	"testing"
)

func TestParseETL(t *testing.T) {
	cases := map[string]uint64{
		"0":                     0,
		"10":                    10 * UnitsPerETL,
		"10.0":                  10 * UnitsPerETL,
		"0.1":                   10_000_000,
		"0.00000001":            1,
		"1.23456789":            123_456_789,
		"007.5":                 750_000_000,
		"184467440737.09551615": math.MaxUint64,
	}
	for s, want := range cases {
		got, err := ParseETL(s)
		if err != nil || got != want {
			t.Errorf("ParseETL(%q) = %d, %v ; attendu %d", s, got, err, want)
		}
	}
}

func TestParseETLRejects(t *testing.T) {
	for _, s := range []string{
		"", ".", "1.", ".5", "-1", "+1", "1e3", "1,5", " 1", "1.2.3",
		"0.000000001",           // 9 décimales
		"184467440737.09551616", // max + 1
		"99999999999999999999",  // dépassement
	} {
		if _, err := ParseETL(s); err == nil {
			t.Errorf("ParseETL(%q) accepté à tort", s)
		}
	}
}

func TestUnitsFromConfig(t *testing.T) {
	cases := map[float64]uint64{
		10.0:       10 * UnitsPerETL,
		0.1:        10_000_000, // 0.1 n'est pas représentable en binaire : aucun arrondi ne doit fuir
		0.3:        30_000_000,
		0.00000001: 1,
	}
	for f, want := range cases {
		got, err := UnitsFromConfig(f)
		if err != nil || got != want {
			t.Errorf("UnitsFromConfig(%v) = %d, %v ; attendu %d", f, got, err, want)
		}
	}
	for _, f := range []float64{-1, math.NaN(), math.Inf(1), 0.000000001} {
		if _, err := UnitsFromConfig(f); err == nil {
			t.Errorf("UnitsFromConfig(%v) accepté à tort", f)
		}
	}
}

func TestFormatETL(t *testing.T) {
	cases := map[uint64]string{
		0:                "0",
		10 * UnitsPerETL: "10",
		500_000:          "0.005",
		1:                "0.00000001",
		123_456_789:      "1.23456789",
	}
	for u, want := range cases {
		if got := FormatETL(u); got != want {
			t.Errorf("FormatETL(%d) = %q, attendu %q", u, got, want)
		}
		if back, _ := ParseETL(FormatETL(u)); back != u {
			t.Errorf("aller-retour %d → %d", u, back)
		}
	}
}

func TestFee(t *testing.T) {
	cases := []struct{ amount, bps, want uint64 }{
		{5 * UnitsPerETL, 10, 500_000}, // 5 ETL à 0.1 % = 0.005 ETL
		{1000, 10, 1},
		{999, 10, 0}, // petit montant : pas de frais (choix assumé)
		{math.MaxUint64, 10, math.MaxUint64 / 1000},
		{math.MaxUint64, 10_000, math.MaxUint64},
		{12345, 0, 0},
	}
	for _, c := range cases {
		got, err := Fee(c.amount, c.bps)
		if err != nil || got != c.want {
			t.Errorf("Fee(%d, %d) = %d, %v ; attendu %d", c.amount, c.bps, got, err, c.want)
		}
	}
	if _, err := Fee(1, 10_001); err != ErrInvalidBps {
		t.Error("bps > 10000 accepté")
	}
}

// Frais exigés : max(0.1 %, 0.0001 ETL). Un paiement de 0.1 ETL ou plus n'est pas touché par le minimum.
func TestRequiredFee(t *testing.T) {
	p := DefaultParams()
	for amount, want := range map[uint64]uint64{
		1:                     10_000, // 1 unité : minimum
		999:                   10_000,
		UnitsPerETL / 100:     10_000, // 0.01 ETL → 0.00001 au taux, relevé au minimum
		UnitsPerETL / 10:      10_000, // 0.1 ETL → exactement le minimum
		UnitsPerETL:           100_000,
		5 * UnitsPerETL:       500_000,
		UnitsPerETL/10 + 1000: 10_001,
	} {
		if got, err := RequiredFee(amount, p); err != nil || got != want {
			t.Errorf("RequiredFee(%s ETL) = %s, attendu %s", FormatETL(amount), FormatETL(got), FormatETL(want))
		}
	}
}
