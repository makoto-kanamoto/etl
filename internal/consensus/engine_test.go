package consensus

import (
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/blockchain/chaintest"
	"github.com/makoto-kanamoto/etl/internal/crypto"
	"github.com/makoto-kanamoto/etl/internal/network"
)

func mustBlock(t *testing.T, s *sim, h uint64) blockchain.Block {
	t.Helper()
	b, err := s.nodes[0].store.BlockAt(h)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestNormalDay(t *testing.T) {
	s := newSim(t)
	s.runDay(1)
	s.requireHeight(1)
	for i, n := range s.nodes {
		if got := n.store.Account(n.wallet.Addr).Balance; got != blockchain.UnitsPerETL {
			t.Errorf("nœud %d : solde %s, attendu 1 (10 ETL ÷ 10)", i, blockchain.FormatETL(got))
		}
	}
	if tip := s.nodes[0].store.Tip(); tip.Round != 0 || len(mustBlock(t, s, 1).Participants) != 10 {
		t.Fatal("bloc du tour 0 avec 10 participants attendu")
	}
	s.runDay(2)
	s.runDay(3)
	s.requireHeight(3)
}

// Une transaction soumise à un nœud parvient au bloc du jour et vide les mempools.
func TestTransactionFlow(t *testing.T) {
	s := newSim(t)
	s.runDay(1)
	from, to := s.nodes[3], s.nodes[7]
	s.at(time.Unix(chaintest.Day(2), 0).Add(-2 * time.Hour))
	tx := s.net.Tx(t, from.wallet, to.wallet.Addr, blockchain.UnitsPerETL/2, 0, s.now.Unix())
	if err := from.e.SubmitTransaction(tx); err != nil {
		t.Fatal(err)
	}
	s.flush()
	for i, n := range s.nodes {
		if n.pool.Len() != 1 {
			t.Fatalf("nœud %d : %d transactions en attente", i, n.pool.Len())
		}
	}
	s.runDay(2)
	s.requireHeight(2)
	if b := mustBlock(t, s, 2); len(b.Txs) != 1 || b.Txs[0].ID != tx.ID {
		t.Fatal("transaction absente du bloc")
	}
	for i, n := range s.nodes {
		if n.pool.Len() != 0 {
			t.Fatalf("nœud %d : mempool non purgé", i)
		}
	}
}

// Le proposant du tour 0 participe puis tombe en panne : le 2e ticket propose au tour 1.
func TestFallbackProposer(t *testing.T) {
	s := newSim(t)
	bt := chaintest.Day(1)
	s.at(time.Unix(bt, 0))
	prev := s.nodes[0].store.Tip()
	best := 0
	for i, n := range s.nodes {
		if blockchain.Ticket(prev.Hash(), n.wallet.Addr).Cmp(blockchain.Ticket(prev.Hash(), s.nodes[best].wallet.Addr)) < 0 {
			best = i
		}
	}
	s.down[best] = true
	for r := 0; r < s.p.MaxRounds; r++ {
		s.at(blockchain.RoundStart(bt, uint8(r), s.p).Add(time.Second))
	}
	s.requireHeight(1)
	other := s.nodes[(best+1)%10]
	if tip := other.store.Tip(); tip.Round != 1 {
		t.Fatalf("finalisé au tour %d, attendu 1", tip.Round)
	}
	if got := other.store.Account(s.nodes[best].wallet.Addr).Balance; got != blockchain.UnitsPerETL {
		t.Fatalf("le proposant défaillant doit garder sa part : %s", blockchain.FormatETL(got))
	}
}

// LE BUG CORRIGÉ : réseau coupé en deux moitiés de 5 le jour 1 → aucun bloc ; le jour 2, réseau
// réparé → la chaîne repart (avant la refonte, elle restait bloquée pour toujours).
func TestPartitionRecovers(t *testing.T) {
	s := newSim(t)
	for i := 5; i < 10; i++ {
		s.group[i] = 1
	}
	s.runDay(1)
	s.requireHeight(0)
	s.group = map[int]int{}
	s.runDay(2)
	s.requireHeight(1)
	if tip := s.nodes[0].store.Tip(); tip.Timestamp != chaintest.Day(2) {
		t.Fatal("le bloc doit être un nouveau bloc du jour 2")
	}
	s.runDay(3)
	s.requireHeight(2)
}

// Verrouillage : tout le monde pré-vote B et s'y verrouille, mais les pré-engagements se perdent.
// Le lendemain, le bloc VERROUILLÉ est re-proposé avec sa preuve et finalisé — jamais un autre.
func TestLockedBlockIsReproposedAndFinalized(t *testing.T) {
	s := newSim(t)
	s.drop = func(d delivery) bool {
		if d.m.Type != network.MsgVote {
			return false
		}
		v, _ := blockchain.DecodeVote(d.m.Payload, s.p.AddressHRP)
		return v.Type == blockchain.Precommit
	}
	s.runDay(1)
	s.requireHeight(0)
	n := s.nodes[0]
	locked, _, ok := n.store.Lock()
	if !ok {
		t.Fatal("aucun verrou alors que > 2/3 ont pré-voté")
	}
	for i, x := range s.nodes {
		b, _, ok := x.store.Lock()
		if !ok || b.Header.Hash() != locked.Header.Hash() {
			t.Fatalf("nœud %d : verrou absent ou différent", i)
		}
	}
	s.drop = nil
	s.runDay(2)
	s.requireHeight(1)
	tip := n.store.Tip()
	if tip.Hash() != locked.Header.Hash() || tip.Timestamp != chaintest.Day(1) {
		t.Fatal("le bloc finalisé doit être le bloc verrouillé du jour 1, avec sa date d'origine")
	}
	if got := n.store.Account(n.wallet.Addr).Balance; got != blockchain.UnitsPerETL {
		t.Fatalf("les participants du jour 1 sont récompensés : %s", blockchain.FormatETL(got))
	}
}

// Un nœud verrouillé, redémarré (mémoire perdue), retrouve son verrou sur disque et refuse
// de pré-voter un autre nouveau bloc.
func TestRestartKeepsLock(t *testing.T) {
	s := newSim(t)
	s.drop = func(d delivery) bool {
		if d.m.Type != network.MsgVote {
			return false
		}
		v, _ := blockchain.DecodeVote(d.m.Payload, s.p.AddressHRP)
		return v.Type == blockchain.Precommit
	}
	s.runDay(1)
	locked, _, ok := s.nodes[4].store.Lock()
	if !ok {
		t.Fatal("prémisse : nœud verrouillé")
	}
	s.restart(4, true)
	s.nodes[4].e.mu.Lock()
	hs := s.nodes[4].e.ensureHeight()
	got := hs.locked
	s.nodes[4].e.mu.Unlock()
	if got == nil || got.block.Header.Hash() != locked.Header.Hash() {
		t.Fatal("verrou perdu au redémarrage")
	}
	s.drop = nil
	s.runDay(2)
	s.requireHeight(1)
	if tip := s.nodes[4].store.Tip(); tip.Hash() != locked.Header.Hash() {
		t.Fatal("le bloc verrouillé doit être finalisé")
	}
}

// Un candidat parrainé par un fondateur est observé puis admis ; il participe ensuite au partage.
func TestRegistrationToAdmission(t *testing.T) {
	s := newSim(t)
	cand := chaintest.NewWallet(t, s.p)
	cn := s.addNode(cand, true)
	s.at(time.Unix(chaintest.Day(1), 0).Add(-time.Hour))
	if err := s.nodes[0].e.SubmitRegistration(s.net.Register(t, cand, s.nodes[0].wallet, 1)); err != nil {
		t.Fatal(err)
	}
	s.flush()
	s.runDay(1)
	s.requireHeight(1)
	if cn.store.Node(cand.Addr).Status != blockchain.StatusCandidate {
		t.Fatal("candidature non inscrite")
	}
	for d := 2; d <= 8; d++ {
		s.runDay(d)
	}
	s.requireHeight(8)
	if got := cn.store.Node(cand.Addr).Status; got != blockchain.StatusAdmitted {
		t.Fatalf("statut %d après observation, attendu admis", got)
	}
	before := cn.store.Account(cand.Addr).Balance
	s.runDay(9)
	if cn.store.Account(cand.Addr).Balance <= before {
		t.Fatal("le nouveau nœud doit recevoir sa part")
	}
}

func TestIPCVoteFlow(t *testing.T) {
	s := newSim(t)
	q4 := blockchain.IPCRecord{Value: 103500, Period: "2026-Q4", SourceSHA256: [32]byte{7}}
	s.at(time.Unix(chaintest.Day(1), 0).Add(-time.Hour))
	for _, n := range s.nodes[:6] {
		_ = n.e.SubmitIPCVote(q4)
	}
	s.flush()
	s.runDay(1)
	var ipc blockchain.IPCRecord
	s.nodes[0].store.ReadState(func(st *blockchain.State) { ipc = st.IPC() })
	if ipc == q4 {
		t.Fatal("6 votes sur 10 ne suffisent pas")
	}
	_ = s.nodes[6].e.SubmitIPCVote(q4)
	s.flush()
	s.runDay(2)
	for i, n := range s.nodes {
		n.store.ReadState(func(st *blockchain.State) { ipc = st.IPC() })
		if ipc != q4 {
			t.Fatalf("nœud %d : IPC non mis à jour", i)
		}
	}
}

// Exclusion votée : la cible participe ce jour-là mais ne touche pas sa part, puis ne participe plus.
func TestExclusionVoteFlow(t *testing.T) {
	s := newSim(t)
	target := s.nodes[9].wallet.Addr
	s.at(time.Unix(chaintest.Day(1), 0).Add(-time.Hour))
	for _, n := range s.nodes[:7] {
		if err := n.e.SubmitExclusionVote(target); err != nil {
			t.Fatal(err)
		}
	}
	s.flush()
	s.runDay(1)
	s.requireHeight(1)
	if s.nodes[0].store.Node(target).Status != blockchain.StatusExcluded {
		t.Fatal("cible non exclue")
	}
	if got := s.nodes[0].store.Account(target).Balance; got != 0 {
		t.Fatalf("la cible a touché sa part : %s", blockchain.FormatETL(got))
	}
	s.runDay(2)
	if b := mustBlock(t, s, 2); len(b.Participants) != 9 {
		t.Fatalf("%d participants, l'exclu ne doit plus participer", len(b.Participants))
	}
}

// Double vote détecté en direct (deux pré-votes au même tour), puis sanctionné au bloc suivant.
func TestDoubleVoteSanctioned(t *testing.T) {
	s := newSim(t)
	bt := chaintest.Day(1)
	s.at(time.Unix(bt, 0))
	cheater := s.nodes[2]
	r := blockchain.RoundID{Day: bt, Round: 0}
	h1 := blockchain.Header{Height: 1, Timestamp: bt}
	h2 := h1
	h2.Round = 1
	v1, _ := blockchain.SignVote(cheater.wallet.Key, blockchain.Prevote, &h1, r, s.p)
	v2, _ := blockchain.SignVote(cheater.wallet.Key, blockchain.Prevote, &h2, r, s.p)
	s.send(2, -1, network.Message{Type: network.MsgVote, Payload: blockchain.EncodeVote(&v1)})
	s.send(2, -1, network.Message{Type: network.MsgVote, Payload: blockchain.EncodeVote(&v2)})
	s.flush()
	s.nodes[0].e.mu.Lock()
	_, got := s.nodes[0].e.veqs[cheater.wallet.Addr.Hash]
	s.nodes[0].e.mu.Unlock()
	if !got {
		t.Fatal("double vote non détecté")
	}
	for r := 0; r < s.p.MaxRounds; r++ {
		s.at(blockchain.RoundStart(bt, uint8(r), s.p).Add(time.Second))
	}
	s.requireHeight(1)
	if s.nodes[0].store.Node(cheater.wallet.Addr).Status != blockchain.StatusExcluded {
		t.Fatal("double vote non sanctionné")
	}
}

// Nœud passif (sans clé) : il suit la chaîne mais ne mine ni ne vote.
func TestPassiveNode(t *testing.T) {
	s := newSim(t)
	obs := s.addNode(chaintest.NewWallet(t, s.p), false)
	s.runDay(1)
	s.requireHeight(1)
	for _, n := range s.nodes {
		for _, m := range n.inbox {
			if m.Type == network.MsgParticipation {
				if pt, _ := blockchain.DecodeParticipation(m.Payload, s.p.AddressHRP); pt.Miner == obs.wallet.Addr {
					t.Fatal("participation d'un nœud passif")
				}
			}
		}
	}
	if err := obs.e.SubmitIPCVote(blockchain.IPCRecord{Value: 1, Period: "2027-Q1"}); err != ErrPassive {
		t.Fatalf("vote IPC d'un passif : err = %v", err)
	}
}

// Après un arrêt sur conflit, le nœud ne mine ni ne vote plus ; les 9 autres suffisent.
func TestHaltedNodeStops(t *testing.T) {
	s := newSim(t)
	s.nodes[0].halted = true
	s.runDay(1)
	if got := s.nodes[1].store.Tip().Height; got != 1 {
		t.Fatalf("hauteur %d", got)
	}
	if got := s.nodes[1].store.Account(s.nodes[0].wallet.Addr).Balance; got != 0 {
		t.Fatal("un nœud arrêté ne doit pas participer")
	}
}

// Une proposition objectivement invalide est rejetée (proposant banni par le réseau).
func TestInvalidProposalRejected(t *testing.T) {
	s := newSim(t)
	bt := chaintest.Day(1)
	s.at(time.Unix(bt, 0))
	n := s.nodes[0]
	n.e.mu.Lock()
	hs := n.e.ensureHeight()
	nom := blockchain.Eligible(hs.day(bt).recv, &hs.prev, bt, s.p, func(a crypto.Address) blockchain.Role { return blockchain.RoleParticipant })
	n.e.mu.Unlock()
	pr := blockchain.Proposal{Round: blockchain.RoundID{Day: bt}, Nomination: nom.Participants,
		Block: blockchain.Block{Header: blockchain.Header{Height: 1, PrevHash: hs.prev.Hash(), Timestamp: bt, Proposer: nom.Participants[0].Miner}}}
	var key = s.nodes[0].wallet.Key
	for _, x := range s.nodes {
		if x.wallet.Addr == pr.Proposer() {
			key = x.wallet.Key
		}
	}
	_ = pr.Block.Sign(key, s.p.ChainID)
	_ = pr.Sign(key, s.p)
	s.now = blockchain.RoundStart(bt, 0, s.p).Add(time.Second)
	if v := s.nodes[1].e.Handle(network.Message{Type: network.MsgProposal, Payload: blockchain.EncodeProposal(&pr)}); v != network.Reject {
		t.Fatalf("proposition invalide : verdict %v, attendu Reject", v)
	}
}
