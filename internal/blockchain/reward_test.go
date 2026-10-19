package blockchain

import "testing"

func TestRewardShare(t *testing.T) {
	p := DefaultParams()
	cases := []struct {
		height uint64
		n      int
		want   uint64
	}{
		{0, 5, 0}, // genesis : zéro pré-mine
		{1, 0, 0},
		{1, 1, 10 * UnitsPerETL},
		{1, 2, 5 * UnitsPerETL},
		{1, 3, 333_333_333}, // 3,33333333 ETL ; 1 unité non émise
		{1, 1000, 1_000_000},
		{1_000_000, 1, 10 * UnitsPerETL}, // émission constante : pas de halving
	}
	for _, c := range cases {
		if got := RewardShare(c.height, c.n, p); got != c.want {
			t.Errorf("RewardShare(h=%d, n=%d) = %s, attendu %s", c.height, c.n, FormatETL(got), FormatETL(c.want))
		}
	}
}

// Quel que soit le nombre de participants, l'émission du jour ne dépasse jamais DailyReward
// et le reste non émis est inférieur à une unité par participant.
func TestRewardShareNeverExceedsDaily(t *testing.T) {
	p := DefaultParams()
	for n := 1; n <= 5000; n++ {
		total := RewardShare(1, n, p) * uint64(n)
		if total > p.DailyReward || p.DailyReward-total >= uint64(n) {
			t.Fatalf("n=%d : émis %d, reste %d", n, total, p.DailyReward-total)
		}
	}
}
