package blockchain

import (
	"context"
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// testParams : paramètres par défaut (10 fondateurs, seuils réels) avec une cible PoW facile.
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

func newWallet(t testing.TB, p Params) wallet {
	t.Helper()
	k, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return wallet{key: k, addr: k.PublicKey().Address(p.AddressHRP)}
}

func newWallets(t testing.TB, p Params, n int) []wallet {
	ws := make([]wallet, n)
	for i := range ws {
		ws[i] = newWallet(t, p)
	}
	return ws
}

func addrs(ws []wallet) []crypto.Address {
	out := make([]crypto.Address, len(ws))
	for i, w := range ws {
		out[i] = w.addr
	}
	return out
}

func makeTx(t testing.TB, p Params, from wallet, to crypto.Address, amount, nonce uint64, ts int64) Transaction {
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

func participate(t testing.TB, p Params, prevHash crypto.Hash, w wallet, blockTime int64) Participation {
	t.Helper()
	pt, err := MineParticipation(context.Background(), w.key, prevHash, blockTime, p)
	if err != nil {
		t.Fatal(err)
	}
	return pt
}

// testNet simule une chaîne : un état, le dernier en-tête, les clés de tous les nœuds.
type testNet struct {
	p        Params
	st       *State
	tip      Header
	founders []wallet
	keys     map[addrKey]*crypto.PrivateKey
}

func newNet(t testing.TB, p Params) *testNet {
	t.Helper()
	n := &testNet{p: p, st: NewState(), founders: newWallets(t, p, p.MinFounders), keys: map[addrKey]*crypto.PrivateKey{}}
	n.track(n.founders...)
	g, err := Genesis(p, GenesisMessage, IPCRecord{Value: 103070, Period: "2026-Q3"}, day(0), addrs(n.founders))
	if err != nil {
		t.Fatal(err)
	}
	if err := n.st.ApplyBlock(&g, p); err != nil {
		t.Fatal(err)
	}
	n.tip = g.Header
	return n
}

func (n *testNet) track(ws ...wallet) {
	for _, w := range ws {
		n.keys[w.addr.Hash] = w.key
	}
}

// build construit (sans l'appliquer) le bloc du jour d : chaque nœud de `nodes` participe,
// réparti entre participants et observations selon son rôle ; edit ajoute le reste.
func (n *testNet) build(t testing.TB, d int, nodes []wallet, edit func(*Block)) Block {
	t.Helper()
	ts := day(d)
	prevHash := n.tip.Hash()
	var recv []Received
	for _, w := range nodes {
		n.track(w)
		recv = append(recv, Received{Participation: participate(t, n.p, prevHash, w, ts), ReceivedAt: time.Unix(ts, 0)})
	}
	dp := Eligible(recv, &n.tip, ts, n.p, func(a crypto.Address) Role { return n.st.Role(a, ts, n.p) })
	b := Block{
		Header:        Header{Height: n.tip.Height + 1, PrevHash: prevHash, Timestamp: ts},
		Participants:  dp.Participants,
		Observations:  dp.Observations,
		Equivocations: dp.Equivocations,
	}
	if len(b.Participants) > 0 {
		b.Header.Proposer = b.Participants[0].Miner
	}
	if edit != nil {
		edit(&b)
	}
	n.finalize(t, &b)
	return b
}

// finalize recalcule les racines et fait signer le bloc par le proposant de l'en-tête.
func (n *testNet) finalize(t testing.TB, b *Block) {
	t.Helper()
	b.Header.TxRoot = TxRoot(b.Txs)
	b.Header.ParticipantsRoot = ParticipantsRoot(b.Participants)
	b.Header.RegistryRoot = RegistryRoot(b.Registrations, b.Observations, b.Equivocations, b.VoteEquivocations)
	b.Header.VotesRoot = VotesRoot(b.IPCVotes, b.Exclusions)
	b.Header.StateRoot = crypto.Hash{}
	if root, err := n.st.PreviewRoot(b, n.p); err == nil { // bloc invalide : racine laissée nulle
		b.Header.StateRoot = root
	}
	if key, ok := n.keys[b.Header.Proposer.Hash]; ok {
		if err := b.Sign(key, n.p.ChainID); err != nil {
			t.Fatal(err)
		}
	}
}

// apply valide puis applique un bloc ; renvoie la première erreur.
func (n *testNet) apply(b *Block) error {
	if err := ValidateBlock(b, &n.tip, n.p, n.st.IsAdmitted); err != nil {
		return err
	}
	if err := n.st.ApplyBlock(b, n.p); err != nil {
		return err
	}
	n.tip = b.Header
	return nil
}

// produce construit et applique le bloc du jour d ; échoue si le bloc est refusé.
func (n *testNet) produce(t testing.TB, d int, nodes []wallet, edit func(*Block)) Block {
	t.Helper()
	b := n.build(t, d, nodes, edit)
	if err := n.apply(&b); err != nil {
		t.Fatalf("bloc du jour %d refusé : %v", d, err)
	}
	return b
}

// register prépare une candidature signée par le candidat et le parrain.
func (n *testNet) register(t testing.TB, cand, sponsor wallet, d int) Registration {
	t.Helper()
	n.track(cand)
	r := Registration{Candidate: cand.addr, Sponsor: sponsor.addr, Day: dayOf(day(d))}
	if err := r.SignCandidate(cand.key, n.p.ChainID); err != nil {
		t.Fatal(err)
	}
	if err := r.SignSponsor(sponsor.key, n.p.ChainID); err != nil {
		t.Fatal(err)
	}
	return r
}

// reactivate prépare une réactivation signée par le nœud inactif.
func (n *testNet) reactivate(t testing.TB, w wallet, d int) Registration {
	t.Helper()
	r := Registration{Candidate: w.addr, Sponsor: crypto.Address{HRP: n.p.AddressHRP}, Day: dayOf(day(d))}
	if err := r.SignCandidate(w.key, n.p.ChainID); err != nil {
		t.Fatal(err)
	}
	return r
}
