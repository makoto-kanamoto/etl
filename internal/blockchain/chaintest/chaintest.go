// Package chaintest génère des chaînes ETL valides pour les tests des autres packages
// (stockage, réseau…). Réservé aux tests : cible PoW facile, clés générées à la volée.
package chaintest

import (
	"context"
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// Params renvoie les paramètres par défaut avec une cible PoW facile.
func Params() blockchain.Params {
	p := blockchain.DefaultParams()
	p.Target = blockchain.TargetFromBits(8)
	return p
}

// Day renvoie 23:00:00 UTC le n-ième jour après le 19/10/2026.
func Day(n int) int64 {
	return time.Date(2026, 10, 19+n, 23, 0, 0, 0, time.UTC).Unix()
}

// Wallet est une clé et son adresse.
type Wallet struct {
	Key  *crypto.PrivateKey
	Addr crypto.Address
}

// NewWallet génère une clé.
func NewWallet(tb testing.TB, p blockchain.Params) Wallet {
	tb.Helper()
	k, err := crypto.GenerateKey()
	if err != nil {
		tb.Fatal(err)
	}
	return Wallet{Key: k, Addr: k.PublicKey().Address(p.AddressHRP)}
}

// Net est une chaîne en cours de construction : genesis, état, dernier en-tête.
type Net struct {
	P        blockchain.Params
	Genesis  blockchain.Block
	State    *blockchain.State
	Tip      blockchain.Header
	Founders []Wallet
	keys     map[crypto.Address]*crypto.PrivateKey
}

// New crée un genesis au jour 0 avec MinFounders fondateurs.
func New(tb testing.TB, p blockchain.Params) *Net {
	tb.Helper()
	n := &Net{P: p, State: blockchain.NewState(), keys: map[crypto.Address]*crypto.PrivateKey{}}
	var founders []crypto.Address
	for i := 0; i < p.MinFounders; i++ {
		w := NewWallet(tb, p)
		n.Founders = append(n.Founders, w)
		n.keys[w.Addr] = w.Key
		founders = append(founders, w.Addr)
	}
	g, err := blockchain.Genesis(p, blockchain.GenesisMessage, blockchain.IPCRecord{Value: 103070, Period: "2026-Q3"}, Day(0), founders)
	if err != nil {
		tb.Fatal(err)
	}
	if err := n.State.ApplyBlock(&g, p); err != nil {
		tb.Fatal(err)
	}
	n.Genesis, n.Tip = g, g.Header
	return n
}

// Build construit le bloc du jour d où participent tous les fondateurs ; edit peut le modifier.
// Le bloc n'est pas appliqué.
func (n *Net) Build(tb testing.TB, d int, edit func(*blockchain.Block)) blockchain.Block {
	tb.Helper()
	ts := Day(d)
	prevHash := n.Tip.Hash()
	var recv []blockchain.Received
	for _, w := range n.Founders {
		pt, err := blockchain.MineParticipation(context.Background(), w.Key, prevHash, ts, n.P)
		if err != nil {
			tb.Fatal(err)
		}
		recv = append(recv, blockchain.Received{Participation: pt, ReceivedAt: time.Unix(ts, 0)})
	}
	dp := blockchain.Eligible(recv, &n.Tip, ts, n.P, func(a crypto.Address) blockchain.Role { return n.State.Role(a, ts, n.P) })
	b := blockchain.Block{
		Header:        blockchain.Header{Height: n.Tip.Height + 1, PrevHash: prevHash, Timestamp: ts, Proposer: dp.Participants[0].Miner},
		Participants:  dp.Participants,
		Observations:  dp.Observations,
		Equivocations: dp.Equivocations,
	}
	if edit != nil {
		edit(&b)
	}
	b.Header.TxRoot = blockchain.TxRoot(b.Txs)
	b.Header.ParticipantsRoot = blockchain.ParticipantsRoot(b.Participants)
	b.Header.RegistryRoot = blockchain.RegistryRoot(b.Registrations, b.Observations, b.Equivocations, b.VoteEquivocations)
	if root, err := n.State.PreviewRoot(&b, n.P); err == nil {
		b.Header.StateRoot = root
	}
	if err := b.Sign(n.keys[b.Header.Proposer], n.P.ChainID); err != nil {
		tb.Fatal(err)
	}
	return b
}

// Next construit le bloc du jour d, le fait finaliser par tous les nœuds admis
// et l'applique à l'état de Net. Renvoie le bloc et son certificat de finalité.
func (n *Net) Next(tb testing.TB, d int, edit func(*blockchain.Block)) (blockchain.Block, blockchain.Certificate) {
	tb.Helper()
	b := n.Build(tb, d, edit)
	if err := blockchain.ValidateBlock(&b, &n.Tip, n.P, n.State.IsAdmitted); err != nil {
		tb.Fatal(err)
	}
	c := n.Certify(tb, &b.Header, len(n.keys))
	if err := n.State.ApplyBlock(&b, n.P); err != nil {
		tb.Fatal(err)
	}
	n.Tip = b.Header
	return b, c
}

// Certify fait voter jusqu'à max nœuds admis (état courant, donc AVANT le bloc) pour l'en-tête h.
func (n *Net) Certify(tb testing.TB, h *blockchain.Header, max int) blockchain.Certificate {
	tb.Helper()
	var votes []blockchain.Vote
	for addr, key := range n.keys {
		if len(votes) == max {
			break
		}
		if !n.State.IsAdmitted(addr) {
			continue
		}
		v, err := blockchain.SignVote(key, h, n.P)
		if err != nil {
			tb.Fatal(err)
		}
		votes = append(votes, v)
	}
	return blockchain.NewCertificate(h, votes, n.P)
}

// Track enregistre la clé d'un portefeuille (pour qu'il puisse voter une fois admis).
func (n *Net) Track(w Wallet) { n.keys[w.Addr] = w.Key }

// Tx crée une transaction signée.
func (n *Net) Tx(tb testing.TB, from Wallet, to crypto.Address, amount, nonce uint64, ts int64) blockchain.Transaction {
	tb.Helper()
	fee, err := blockchain.Fee(amount, n.P.FeeBps)
	if err != nil {
		tb.Fatal(err)
	}
	tx := blockchain.Transaction{From: from.Addr, To: to, Amount: amount, Fee: fee, Nonce: nonce, Timestamp: ts}
	if err := tx.Sign(from.Key, n.P.ChainID); err != nil {
		tb.Fatal(err)
	}
	return tx
}

// Register crée une candidature signée par le candidat et contresignée par le parrain.
func (n *Net) Register(tb testing.TB, cand, sponsor Wallet, d int) blockchain.Registration {
	tb.Helper()
	r := blockchain.Registration{Candidate: cand.Addr, Sponsor: sponsor.Addr, Day: Day(d) / 86400}
	if err := r.SignCandidate(cand.Key, n.P.ChainID); err != nil {
		tb.Fatal(err)
	}
	if err := r.SignSponsor(sponsor.Key, n.P.ChainID); err != nil {
		tb.Fatal(err)
	}
	return r
}
