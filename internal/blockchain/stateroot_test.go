package blockchain

import (
	"testing"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

func TestStateRootCoversEveryField(t *testing.T) {
	var k1, k2 addrKey
	k1[0], k2[0] = 1, 2
	eid := crypto.Hash{9}
	terms := EscrowTerms{Seller: crypto.Address{Hash: k1}, Buyer: crypto.Address{Hash: k2}, Amount: 7, Fee: 1, Days: 3, Timestamp: 5}
	base := func() *State {
		return NewStateFrom(
			map[addrKey]Account{k1: {Balance: 5, Nonce: 1}},
			map[addrKey]Node{k2: {Status: StatusAdmitted, AdmittedDay: 10, LastParticipationDay: 12}},
			IPCRecord{Value: 100000, Period: "2026-Q3"}, IPCRecord{Value: 103070, Period: "2026-Q4"},
			map[ParamID]uint64{ParamFeeBps: 10}, map[ParamID]PendingParam{ParamDailyReward: {Value: 5, ActivationDay: 100}},
			EscrowData{
				Escrows:     map[crypto.Hash]Escrow{eid: {Terms: terms, Deadline: 20}},
				Reputations: map[addrKey]Reputation{k1: {Completed: 1}},
			},
		)
	}
	ref := base().Root()
	mutations := map[string]func(*State){
		"solde":                func(s *State) { s.accounts[k1] = Account{Balance: 6, Nonce: 1} },
		"nonce":                func(s *State) { s.accounts[k1] = Account{Balance: 5, Nonce: 2} },
		"nouveau compte":       func(s *State) { s.accounts[k2] = Account{} },
		"statut":               func(s *State) { n := s.nodes[k2]; n.Status = StatusInactive; s.nodes[k2] = n },
		"fondateur":            func(s *State) { n := s.nodes[k2]; n.Founder = true; s.nodes[k2] = n },
		"parrain":              func(s *State) { n := s.nodes[k2]; n.Sponsor = k1; s.nodes[k2] = n },
		"dernière présence":    func(s *State) { n := s.nodes[k2]; n.LastParticipationDay++; s.nodes[k2] = n },
		"interdiction":         func(s *State) { n := s.nodes[k2]; n.SponsorBanUntil = 1; s.nodes[k2] = n },
		"compte ↔ nœud":        func(s *State) { s.nodes[k1] = s.nodes[k2]; delete(s.nodes, k2) },
		"nœud supprimé":        func(s *State) { delete(s.nodes, k2) },
		"IPC courant":          func(s *State) { s.ipc.Value++ },
		"période IPC":          func(s *State) { s.ipc.Period = "2027-Q1" },
		"source IPC":           func(s *State) { s.ipc.SourceSHA256[0] = 1 },
		"IPC de référence":     func(s *State) { s.baseIPC.Value++ },
		"paramètre en vigueur": func(s *State) { s.gov.values[ParamFeeBps] = 11 },
		"changement en attente": func(s *State) {
			s.gov.pending[ParamDailyReward] = PendingParam{Value: 6, ActivationDay: 100}
		},
		"date d'activation": func(s *State) {
			s.gov.pending[ParamDailyReward] = PendingParam{Value: 5, ActivationDay: 101}
		},
		"escrow : montant":             func(s *State) { e := s.escrows[eid]; e.Terms.Amount++; s.escrows[eid] = e },
		"escrow : arbitre":             func(s *State) { e := s.escrows[eid]; e.Terms.Arbiter.Hash[0] = 3; s.escrows[eid] = e },
		"escrow : rémunération":        func(s *State) { e := s.escrows[eid]; e.Terms.ArbiterFee = 1; s.escrows[eid] = e },
		"escrow : délai":               func(s *State) { e := s.escrows[eid]; e.Terms.Days++; s.escrows[eid] = e },
		"escrow : échéance":            func(s *State) { e := s.escrows[eid]; e.Deadline++; s.escrows[eid] = e },
		"escrow : séquence":            func(s *State) { e := s.escrows[eid]; e.Seq++; s.escrows[eid] = e },
		"escrow : payé":                func(s *State) { e := s.escrows[eid]; e.Paid = true; s.escrows[eid] = e },
		"escrow : litige":              func(s *State) { e := s.escrows[eid]; e.Disputed = true; s.escrows[eid] = e },
		"escrow : issue":               func(s *State) { e := s.escrows[eid]; e.Outcome = EscrowRefunded; s.escrows[eid] = e },
		"escrow : jour de clôture":     func(s *State) { e := s.escrows[eid]; e.ClosedDay = 1; s.escrows[eid] = e },
		"escrow : identifiant":         func(s *State) { s.escrows[crypto.Hash{8}] = s.escrows[eid]; delete(s.escrows, eid) },
		"escrow supprimé":              func(s *State) { delete(s.escrows, eid) },
		"réputation : menés":           func(s *State) { s.reps[k1] = Reputation{Completed: 2} },
		"réputation : remboursés":      func(s *State) { s.reps[k1] = Reputation{Completed: 1, Refunded: 1} },
		"réputation : litiges ouverts": func(s *State) { s.reps[k1] = Reputation{Completed: 1, DisputesOpened: 1} },
		"réputation : litiges perdus":  func(s *State) { s.reps[k1] = Reputation{Completed: 1, DisputesLost: 1} },
		"réputation : autre adresse":   func(s *State) { s.reps[k2] = s.reps[k1]; delete(s.reps, k1) },
	}
	for name, mutate := range mutations {
		s := base()
		mutate(s)
		if s.Root() == ref {
			t.Errorf("%s : non couvert par la racine", name)
		}
	}
}

// La racine ne dépend que du contenu, pas de l'ordre d'insertion.
func TestStateRootDeterministic(t *testing.T) {
	a, b := NewState(), NewState()
	for i := byte(0); i < 50; i++ {
		a.accounts[addrKey{i}] = Account{Balance: uint64(i)}
		b.accounts[addrKey{49 - i}] = Account{Balance: uint64(49 - i)}
	}
	if a.Root() != b.Root() {
		t.Fatal("racine dépendante de l'ordre d'insertion")
	}
}

func TestApplyBlockChecksStateRoot(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	b := n.build(t, 1, n.founders, nil)
	if b.Header.StateRoot != n.mustPreview(t, &b) {
		t.Fatal("prémisse : racine annoncée = racine calculée")
	}
	b.Header.StateRoot[0] ^= 1
	n.finalize2(t, &b) // re-signer sans recalculer la racine
	before, nodes := n.st.accountsCopy(), n.st.nodes.clone()
	if err := n.st.ApplyBlock(&b, p); err != ErrStateRoot {
		t.Fatalf("err = %v", err)
	}
	if !equalMaps(before, n.st.accounts) || !equalMaps(nodes, n.st.nodes) {
		t.Fatal("état modifié malgré une racine fausse")
	}
}

func TestGenesisStateRoot(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	if n.st.Root() != n.tip.StateRoot {
		t.Fatal("racine du genesis ≠ état après genesis")
	}
	n.produce(t, 1, n.founders, nil)
	if n.st.Root() != n.tip.StateRoot {
		t.Fatal("racine du bloc 1 ≠ état après le bloc 1")
	}
}

func TestChangesDiff(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	cand := newWallet(t, p)
	n.produce(t, 1, n.founders, withRegs(n.register(t, cand, n.founders[0], 1)))

	// Jour 8 : fin d'observation sans aucune présence → le candidat est retiré du registre.
	b := n.build(t, 8, n.founders[:9], nil)
	c, err := n.st.Prepare(&b, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.DeletedNodes) != 1 || c.DeletedNodes[0] != cand.addr.Hash {
		t.Fatalf("suppressions : %v", c.DeletedNodes)
	}
	for _, w := range n.founders[:9] {
		if _, ok := c.Nodes[w.addr.Hash]; !ok {
			t.Error("présence d'un participant non reportée")
		}
		if _, ok := c.Accounts[w.addr.Hash]; !ok {
			t.Error("récompense d'un participant non reportée")
		}
	}
	if _, ok := c.Nodes[n.founders[9].addr.Hash]; ok {
		t.Error("nœud inchangé reporté comme modifié")
	}
	// Prepare ne modifie rien ; Commit applique.
	if n.st.Status(cand.addr) != StatusCandidate {
		t.Fatal("Prepare a modifié l'état")
	}
	n.st.Commit(c)
	if n.st.Status(cand.addr) != StatusUnknown || n.st.Root() != c.Root {
		t.Fatal("Commit incomplet")
	}
}

func (n *testNet) mustPreview(t testing.TB, b *Block) crypto.Hash {
	t.Helper()
	r, err := n.st.PreviewRoot(b, n.p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// finalize2 re-signe le bloc sans toucher aux racines.
func (n *testNet) finalize2(t testing.TB, b *Block) {
	t.Helper()
	if err := b.Sign(n.keys[b.Header.Proposer.Hash], n.p.ChainID); err != nil {
		t.Fatal(err)
	}
}
