package blockchain

import (
	"context"
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// BenchmarkPoWHash mesure le débit de hachage de la PoW sur un cœur.
// Temps moyen de minage ≈ 2^DefaultTargetBits / débit.
func BenchmarkPoWHash(b *testing.B) {
	pt := Participation{Miner: crypto.Address{HRP: "etl"}}
	var prev crypto.Hash
	for i := 0; i < b.N; i++ {
		pt.Nonce = uint64(i)
		_ = pt.PoWHash(prev, 82800)
	}
}

// TestCalibration mine quelques participations à la difficulté par défaut et affiche le temps moyen.
// Lancer : go test ./internal/blockchain -run Calibration -v -calibrate
func TestCalibration(t *testing.T) {
	if !*calibrate {
		t.Skip("lancer avec -calibrate")
	}
	p := DefaultParams()
	key, _ := crypto.GenerateKey()
	const runs = 20
	var total time.Duration
	for i := 0; i < runs; i++ {
		var prev crypto.Hash
		prev[0] = byte(i)
		start := time.Now()
		if _, err := MineParticipation(context.Background(), key, prev, 82800, p); err != nil {
			t.Fatal(err)
		}
		total += time.Since(start)
	}
	t.Logf("%d bits : temps moyen = %v", DefaultTargetBits, total/runs)
}
