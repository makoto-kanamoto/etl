package blockchain

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// testParams : paramètres par défaut avec une cible facile pour des tests rapides.
func testParams() Params {
	p := DefaultParams()
	p.Target = TargetFromBits(8)
	return p
}

// day(n) : 23:00:00 UTC le n-ième jour après le 19/10/2026.
func day(n int) int64 {
	return time.Date(2026, 10, 19+n, 23, 0, 0, 0, time.UTC).Unix()
}

type wallet struct {
	key  *crypto.PrivateKey
	addr crypto.Address
}

func newWallet(t *testing.T, p Params) wallet {
	t.Helper()
	k, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return wallet{key: k, addr: k.PublicKey().Address(p.AddressHRP)}
}

func admitAll(crypto.Address) bool { return true }

func testGenesis(t *testing.T, p Params) Block {
	t.Helper()
	g, err := Genesis(p, GenesisMessage, IPCRecord{Value: 103070, Period: "2026-Q3"}, day(0))
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func makeTx(t *testing.T, p Params, from wallet, to crypto.Address, amount, nonce uint64, ts int64) Transaction {
	t.Helper()
	fee, err := Fee(amount, p.FeeBps)
	if err != nil {
		t.Fatal(err)
	}
	tx := Transaction{From: from.addr, To: to, Amount: amount, Fee: fee, Nonce: nonce, Timestamp: ts}
	if err := tx.Sign(from.key, p.ChainID); err != nil {
		t.Fatal(err)
	}
	return tx
}

func participate(t *testing.T, p Params, prev *Header, w wallet, blockTime int64) Participation {
	t.Helper()
	pt, err := MineParticipation(context.Background(), w.key, prev.Hash(), blockTime, p)
	if err != nil {
		t.Fatal(err)
	}
	return pt
}

// sortByTicket trie les participants comme l'exige le protocole.
func sortByTicket(pts []Participation, prevHash crypto.Hash) {
	sort.Slice(pts, func(i, j int) bool {
		return Ticket(prevHash, pts[i].Miner).Cmp(Ticket(prevHash, pts[j].Miner)) < 0
	})
}

// finalize recalcule les racines et fait signer le bloc par le proposant désigné dans l'en-tête.
func finalize(t *testing.T, p Params, b *Block, keys map[addrKey]*crypto.PrivateKey) {
	t.Helper()
	b.Header.TxRoot = TxRoot(b.Txs)
	b.Header.ParticipantsRoot = ParticipantsRoot(b.Participants)
	if err := b.Sign(keys[b.Header.Proposer.Hash], p.ChainID); err != nil {
		t.Fatal(err)
	}
}

// buildBlock construit un bloc valide au-dessus de prev : chaque nœud participe,
// le ticket le plus bas propose.
func buildBlock(t *testing.T, p Params, prev *Header, nodes []wallet, txs []Transaction, ts int64) (Block, map[addrKey]*crypto.PrivateKey) {
	t.Helper()
	keys := map[addrKey]*crypto.PrivateKey{}
	var pts []Participation
	for _, w := range nodes {
		keys[w.addr.Hash] = w.key
		pts = append(pts, participate(t, p, prev, w, ts))
	}
	sortByTicket(pts, prev.Hash())
	b := Block{
		Header: Header{
			Height:    prev.Height + 1,
			PrevHash:  prev.Hash(),
			Timestamp: ts,
			Proposer:  pts[0].Miner,
		},
		Participants: pts,
		Txs:          txs,
	}
	finalize(t, p, &b, keys)
	return b, keys
}
