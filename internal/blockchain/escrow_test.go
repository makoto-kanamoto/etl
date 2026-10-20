package blockchain

import (
	"bytes"
	"testing"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// escrowNet : chaîne de 10 fondateurs ayant chacun 1 ETL (jour 1) ; vendeur et arbitre sont des
// fondateurs, l'acheteur un nouveau venu sans compte.
type escrowNet struct {
	*testNet
	seller, arbiter, buyer wallet
	d                      int // dernier jour produit
}

func newEscrowNet(t *testing.T) *escrowNet {
	t.Helper()
	n := newNet(t, testParams())
	n.produce(t, 1, n.founders, nil)
	return &escrowNet{testNet: n, seller: n.founders[0], arbiter: n.founders[1], buyer: newWallet(t, n.p), d: 1}
}

func (n *escrowNet) terms(withArbiter bool, amount uint64, days uint8) EscrowTerms {
	fee, _ := RequiredFee(amount, n.p)
	t := EscrowTerms{Seller: n.seller.addr, Buyer: n.buyer.addr, Amount: amount, Fee: fee, Days: days, Timestamp: day(n.d)}
	if withArbiter {
		t.Arbiter, t.ArbiterFee = n.arbiter.addr, 1000
	}
	return t
}

// sign signe op avec les portefeuilles donnés, chacun dans son rôle.
func (n *escrowNet) sign(t *testing.T, op EscrowOp, signers ...uint8) EscrowOp {
	t.Helper()
	for _, s := range signers {
		w := map[uint8]wallet{SignerSeller: n.seller, SignerBuyer: n.buyer, SignerArbiter: n.arbiter}[s]
		if err := op.Sign(w.key, s, n.p); err != nil {
			t.Fatal(err)
		}
	}
	return op
}

func (n *escrowNet) open(t *testing.T, terms EscrowTerms) EscrowOp {
	signers := []uint8{SignerSeller}
	if terms.HasArbiter() {
		signers = append(signers, SignerArbiter)
	}
	return n.sign(t, EscrowOp{Kind: EscrowOpen, Terms: terms}, signers...)
}

func (n *escrowNet) op(t *testing.T, kind EscrowKind, id crypto.Hash, seq uint32, signers ...uint8) EscrowOp {
	return n.sign(t, EscrowOp{Kind: kind, Escrow: id, Seq: seq}, signers...)
}

// next produit le bloc du jour suivant (ou du jour d s'il est donné) avec ces opérations.
func (n *escrowNet) next(t *testing.T, ops ...EscrowOp) Block {
	t.Helper()
	n.d++
	return n.produce(t, n.d, n.founders, func(b *Block) { b.EscrowOps = ops })
}

// refuse vérifie qu'un bloc du jour suivant contenant ces opérations est refusé avec want.
func (n *escrowNet) refuse(t *testing.T, want error, ops ...EscrowOp) {
	t.Helper()
	b := n.build(t, n.d+1, n.founders, func(b *Block) { b.EscrowOps = ops })
	if err := n.apply(&b); err != want {
		t.Fatalf("err = %v, attendu %v", err, want)
	}
}

func (n *escrowNet) balance(w wallet) uint64 { return n.st.Account(w.addr).Balance }

func (n *escrowNet) escrow(t *testing.T, id crypto.Hash) Escrow {
	t.Helper()
	e, ok := n.st.Escrow(id)
	if !ok {
		t.Fatal("échange absent de l'état")
	}
	return e
}

func TestEscrowOpenLocksFundsAndPaysFee(t *testing.T) {
	n := newEscrowNet(t)
	terms := n.terms(true, UnitsPerETL/2, 3)
	before := n.balance(n.seller)
	b := n.next(t, n.open(t, terms))
	e := n.escrow(t, terms.ID())
	if e.Deadline != dayOf(day(2))+3 || e.Seq != 0 || e.Outcome != EscrowPending {
		t.Fatalf("échange : %+v", e)
	}
	share := RewardShare(b.Header.Height, len(b.Participants), n.p)
	cost := terms.Amount + terms.ArbiterFee + terms.Fee
	got := n.balance(n.seller)
	want := before - cost + share
	if b.Header.Proposer == n.seller.addr {
		want += terms.Fee
	}
	if got != want {
		t.Fatalf("solde du vendeur : %d, attendu %d", got, want)
	}
	if terms.Fee != UnitsPerETL/2*10/10000 {
		t.Fatalf("frais : %d", terms.Fee)
	}
}

// Les frais d'ouverture vont au proposant du bloc, même quand ce n'est pas le vendeur.
func TestEscrowOpenFeeToProposer(t *testing.T) {
	n := newEscrowNet(t)
	probe := n.build(t, 2, n.founders, nil)
	if probe.Header.Proposer == n.seller.addr { // vendeur ≠ proposant du jour 2
		n.seller, n.arbiter = n.founders[1], n.founders[2]
	}
	terms := n.terms(false, UnitsPerETL/2, 3)
	b := n.build(t, 2, n.founders, func(b *Block) { b.EscrowOps = []EscrowOp{n.open(t, terms)} })
	proposer := b.Header.Proposer
	before := n.st.Account(proposer).Balance
	if err := n.apply(&b); err != nil {
		t.Fatal(err)
	}
	share := RewardShare(b.Header.Height, len(b.Participants), n.p)
	if proposer == n.seller.addr || n.st.Account(proposer).Balance != before+share+terms.Fee {
		t.Fatalf("proposant : +%d, attendu +%d (part) +%d (frais)", n.st.Account(proposer).Balance-before, share, terms.Fee)
	}
}

func TestEscrowOpenRejects(t *testing.T) {
	n := newEscrowNet(t)
	outsider := newWallet(t, n.p)
	mod := func(f func(*EscrowTerms)) EscrowTerms {
		x := n.terms(true, UnitsPerETL/2, 3)
		f(&x)
		return x
	}
	cases := map[string]struct {
		op   EscrowOp
		want error
	}{
		"montant nul":                   {n.open(t, mod(func(x *EscrowTerms) { x.Amount, x.Fee = 0, n.p.MinFee })), ErrEscrowTerms},
		"frais inexacts":                {n.open(t, mod(func(x *EscrowTerms) { x.Fee++ })), ErrEscrowTerms},
		"vendeur = acheteur":            {n.open(t, mod(func(x *EscrowTerms) { x.Buyer = n.seller.addr })), ErrEscrowTerms},
		"délai nul":                     {n.open(t, mod(func(x *EscrowTerms) { x.Days = 0 })), ErrEscrowTerms},
		"délai de 31 jours":             {n.open(t, mod(func(x *EscrowTerms) { x.Days = 31 })), ErrEscrowTerms},
		"rémunération sans arbitre":     {n.open(t, func() EscrowTerms { x := n.terms(false, UnitsPerETL/2, 3); x.ArbiterFee = 1; return x }()), ErrEscrowTerms},
		"postérieure au bloc":           {n.open(t, mod(func(x *EscrowTerms) { x.Timestamp = day(2) + 1 })), ErrEscrowExpired},
		"périmée (7 jours)":             {n.open(t, mod(func(x *EscrowTerms) { x.Timestamp = day(2) - 7*86400 })), ErrEscrowExpired},
		"solde insuffisant":             {n.open(t, mod(func(x *EscrowTerms) { x.Amount = 2 * UnitsPerETL; x.Fee, _ = RequiredFee(x.Amount, n.p) })), ErrInsufficientFunds},
		"sans cosignature de l'arbitre": {n.sign(t, EscrowOp{Kind: EscrowOpen, Terms: n.terms(true, UnitsPerETL/2, 3)}, SignerSeller), ErrEscrowSigners},
		"cosignée par un autre que l'arbitre": {func() EscrowOp {
			op := n.sign(t, EscrowOp{Kind: EscrowOpen, Terms: n.terms(true, UnitsPerETL/2, 3)}, SignerSeller)
			if err := op.Sign(n.founders[2].key, SignerArbiter, n.p); err != nil {
				t.Fatal(err)
			}
			return op
		}(), ErrEscrowSigners},
		"signataire superflu (acheteur)": {n.sign(t, EscrowOp{Kind: EscrowOpen, Terms: n.terms(false, UnitsPerETL/2, 3)}, SignerSeller, SignerBuyer), ErrEscrowSigners},
		"signée par l'acheteur":          {n.sign(t, EscrowOp{Kind: EscrowOpen, Terms: n.terms(false, UnitsPerETL/2, 3)}, SignerBuyer), ErrEscrowSigners},
		"séquence non nulle": {func() EscrowOp {
			op := EscrowOp{Kind: EscrowOpen, Terms: n.terms(false, UnitsPerETL/2, 3), Seq: 1}
			return n.sign(t, op, SignerSeller)
		}(), ErrEscrowTerms},
	}
	// Arbitre non admis, ou partie à l'échange.
	n.arbiter = outsider
	cases["arbitre non admis"] = struct {
		op   EscrowOp
		want error
	}{n.open(t, n.terms(true, UnitsPerETL/2, 3)), ErrEscrowTerms}
	n.arbiter = n.seller
	cases["arbitre = vendeur"] = struct {
		op   EscrowOp
		want error
	}{n.sign(t, EscrowOp{Kind: EscrowOpen, Terms: n.terms(true, UnitsPerETL/2, 3)}, SignerSeller, SignerArbiter), ErrEscrowTerms}
	n.arbiter, n.buyer = n.founders[2], n.founders[2] // acheteur admis, arbitre = acheteur
	cases["arbitre = acheteur"] = struct {
		op   EscrowOp
		want error
	}{n.open(t, n.terms(true, UnitsPerETL/2, 3)), ErrEscrowTerms}
	n.arbiter, n.buyer = n.founders[1], newWallet(t, n.p)
	for name, c := range cases {
		b := n.build(t, 2, n.founders, func(b *Block) { b.EscrowOps = []EscrowOp{c.op} })
		if err := n.apply(&b); err != c.want {
			t.Errorf("%s : err = %v, attendu %v", name, err, c.want)
		}
	}
	// La même ouverture deux fois : refusée (pas de double blocage, pas de rejeu).
	op := n.open(t, n.terms(false, UnitsPerETL/4, 3))
	n.refuse(t, ErrEscrowUnknown, op, op)
	n.next(t, op)
	n.refuse(t, ErrEscrowUnknown, op)
}

// Échange avec arbitre, mené à terme par le vendeur : l'acheteur reçoit, la rémunération revient au vendeur.
func TestEscrowReleasedBySeller(t *testing.T) {
	n := newEscrowNet(t)
	terms := n.terms(true, UnitsPerETL/2, 3)
	id := terms.ID()
	n.next(t, n.open(t, terms))
	n.next(t, n.op(t, EscrowPaid, id, 0, SignerBuyer))
	sellerBefore := n.balance(n.seller)
	b := n.next(t, n.op(t, EscrowRelease, id, 1, SignerSeller))
	e := n.escrow(t, id)
	if e.Outcome != EscrowReleased || !e.Paid || e.Seq != 2 || e.ClosedDay != dayOf(b.Header.Timestamp) {
		t.Fatalf("échange : %+v", e)
	}
	if n.balance(n.buyer) != terms.Amount {
		t.Fatalf("acheteur : %d", n.balance(n.buyer))
	}
	share := RewardShare(b.Header.Height, len(b.Participants), n.p)
	if n.balance(n.seller) != sellerBefore+terms.ArbiterFee+share {
		t.Fatal("rémunération non rendue au vendeur (l'arbitre n'est pas intervenu)")
	}
	for _, w := range []wallet{n.seller, n.buyer} {
		if r := n.st.Reputation(w.addr); r != (Reputation{Completed: 1}) {
			t.Fatalf("réputation : %+v", r)
		}
	}
	// Plus aucune opération une fois réglé.
	n.refuse(t, ErrEscrowClosed, n.op(t, EscrowRefund, id, 2, SignerBuyer))
}

// Litige tranché par l'arbitre en faveur de l'acheteur (2 sur 3) : rémunération à l'arbitre,
// litige perdu pour le vendeur.
func TestEscrowArbiterReleases(t *testing.T) {
	n := newEscrowNet(t)
	terms := n.terms(true, UnitsPerETL/2, 3)
	id := terms.ID()
	n.next(t, n.open(t, terms))
	n.next(t, n.op(t, EscrowPaid, id, 0, SignerBuyer), n.op(t, EscrowDispute, id, 1, SignerBuyer))
	if e := n.escrow(t, id); !e.Disputed || e.Seq != 2 {
		t.Fatalf("litige : %+v", e)
	}
	// Seuls, ni l'arbitre ni l'acheteur ne libèrent ; un second litige est refusé.
	n.refuse(t, ErrEscrowSigners, n.op(t, EscrowRelease, id, 2, SignerArbiter))
	n.refuse(t, ErrEscrowSigners, n.op(t, EscrowRelease, id, 2, SignerBuyer))
	n.refuse(t, ErrEscrowState, n.op(t, EscrowDispute, id, 2, SignerSeller))
	arbBefore := n.balance(n.arbiter)
	b := n.next(t, n.op(t, EscrowRelease, id, 2, SignerBuyer, SignerArbiter))
	share := RewardShare(b.Header.Height, len(b.Participants), n.p)
	if n.balance(n.buyer) != terms.Amount || n.balance(n.arbiter) != arbBefore+terms.ArbiterFee+share {
		t.Fatalf("acheteur %d, arbitre %d", n.balance(n.buyer), n.balance(n.arbiter)-arbBefore)
	}
	if r := n.st.Reputation(n.seller.addr); r != (Reputation{Completed: 1, DisputesLost: 1}) {
		t.Fatalf("vendeur : %+v", r)
	}
	if r := n.st.Reputation(n.buyer.addr); r != (Reputation{Completed: 1, DisputesOpened: 1}) {
		t.Fatalf("acheteur : %+v", r)
	}
}

// Remboursements : l'acheteur seul, ou le vendeur avec l'arbitre (litige perdu pour l'acheteur).
func TestEscrowRefunds(t *testing.T) {
	n := newEscrowNet(t)
	t1, t2 := n.terms(true, UnitsPerETL/4, 3), n.terms(true, UnitsPerETL/5, 3)
	n.next(t, n.open(t, t1), n.open(t, t2))
	n.next(t, n.op(t, EscrowPaid, t2.ID(), 0, SignerBuyer))
	// Avant l'échéance, le vendeur seul ne rembourse pas.
	n.refuse(t, ErrEscrowSigners, n.op(t, EscrowRefund, t1.ID(), 0, SignerSeller))
	n.next(t, n.op(t, EscrowRefund, t1.ID(), 0, SignerBuyer), n.op(t, EscrowRefund, t2.ID(), 1, SignerSeller, SignerArbiter))
	if n.escrow(t, t1.ID()).Outcome != EscrowRefunded || n.escrow(t, t2.ID()).Outcome != EscrowRefunded {
		t.Fatal("remboursements non appliqués")
	}
	if r := n.st.Reputation(n.buyer.addr); r != (Reputation{Refunded: 2, DisputesLost: 1}) {
		t.Fatalf("acheteur : %+v", r)
	}
	if n.balance(n.buyer) != 0 {
		t.Fatal("l'acheteur ne reçoit rien d'un remboursement")
	}
}

// Échéance : sans « payé », le vendeur seul est remboursé ; « payé » n'est plus possible.
// Avec « payé », l'échéance ne rembourse plus le vendeur (mode simple : fonds bloqués jusqu'à accord).
func TestEscrowDeadline(t *testing.T) {
	n := newEscrowNet(t)
	unpaid, paid := n.terms(false, UnitsPerETL/4, 1), n.terms(false, UnitsPerETL/5, 3)
	n.next(t, n.open(t, unpaid), n.open(t, paid)) // jour 2, échéance jour 3
	n.refuse(t, ErrEscrowState, n.op(t, EscrowPaid, unpaid.ID(), 0, SignerBuyer))
	n.next(t, n.op(t, EscrowRefund, unpaid.ID(), 0, SignerSeller))
	if n.escrow(t, unpaid.ID()).Outcome != EscrowRefunded {
		t.Fatal("remboursement après échéance refusé")
	}
	// « Payé » la veille de l'échéance : accepté.
	n.next(t, n.op(t, EscrowPaid, paid.ID(), 0, SignerBuyer))
	_ = paid
}

func TestEscrowSimpleModeAfterPaid(t *testing.T) {
	n := newEscrowNet(t)
	terms := n.terms(false, UnitsPerETL/4, 2)
	id := terms.ID()
	n.next(t, n.open(t, terms))
	n.next(t, n.op(t, EscrowPaid, id, 0, SignerBuyer))
	n.next(t)
	n.next(t) // échéance passée
	n.refuse(t, ErrEscrowSigners, n.op(t, EscrowRefund, id, 1, SignerSeller))
	n.refuse(t, ErrEscrowSigners, n.op(t, EscrowRelease, id, 1, SignerBuyer, SignerArbiter))
	n.next(t, n.op(t, EscrowRelease, id, 1, SignerSeller))
	if n.balance(n.buyer) != terms.Amount {
		t.Fatal("libération par le vendeur refusée")
	}
}

func TestEscrowSequenceAndReplay(t *testing.T) {
	n := newEscrowNet(t)
	terms := n.terms(true, UnitsPerETL/4, 3)
	id := terms.ID()
	n.next(t, n.open(t, terms))
	n.refuse(t, ErrEscrowSigners, n.op(t, EscrowPaid, id, 0, SignerSeller)) // « payé » : l'acheteur seul
	paid := n.op(t, EscrowPaid, id, 0, SignerBuyer)
	n.refuse(t, ErrEscrowSeq, n.op(t, EscrowPaid, id, 1, SignerBuyer))
	n.next(t, paid)
	n.refuse(t, ErrEscrowSeq, paid)                                      // rejeu : la séquence a avancé
	n.refuse(t, ErrEscrowState, n.op(t, EscrowPaid, id, 1, SignerBuyer)) // un seul « payé »
	n.refuse(t, ErrEscrowUnknown, n.op(t, EscrowPaid, crypto.Hash{1}, 0, SignerBuyer))
	n.refuse(t, ErrEscrowSigners, n.op(t, EscrowDispute, id, 1, SignerArbiter))
	// Un « payé » signé par le vendeur, ou un litige signé par l'arbitre : refusés.
	n.refuse(t, ErrEscrowSigners, n.op(t, EscrowDispute, id, 1, SignerArbiter))
	n.refuse(t, ErrEscrowSigners, n.op(t, EscrowDispute, id, 1, SignerSeller, SignerBuyer))
	// Signatures dans le désordre (acheteur et arbitre inversés) : rôles faux, refusé.
	n.next(t, n.op(t, EscrowDispute, id, 1, SignerBuyer))
	swapped := n.op(t, EscrowRelease, id, 2, SignerBuyer, SignerArbiter)
	swapped.Signatures[0], swapped.Signatures[1] = swapped.Signatures[1], swapped.Signatures[0]
	n.refuse(t, ErrEscrowSigners, swapped)
	// Signature d'un tiers à la place de l'acheteur.
	other := n.buyer
	n.buyer = newWallet(t, n.p)
	forged := n.op(t, EscrowRelease, id, 2, SignerBuyer, SignerArbiter)
	n.buyer = other
	n.refuse(t, ErrEscrowSigners, forged)
}

// Un arbitre exclu du registre perd son pouvoir d'arbitrage.
func TestEscrowExcludedArbiter(t *testing.T) {
	n := newEscrowNet(t)
	terms := n.terms(true, UnitsPerETL/4, 3)
	id := terms.ID()
	n.next(t, n.open(t, terms))
	k := n.st.nodes[n.arbiter.addr.Hash]
	k.Status = StatusExcluded
	n.st.nodes[n.arbiter.addr.Hash] = k
	n.founders = append([]wallet{n.founders[0]}, n.founders[2:]...) // l'exclu ne participe plus
	n.refuse(t, ErrEscrowSigners, n.op(t, EscrowRelease, id, 0, SignerBuyer, SignerArbiter))
}

// Un échange réglé est élagué 90 jours après ; la réputation reste.
func TestEscrowPruning(t *testing.T) {
	n := newEscrowNet(t)
	terms := n.terms(false, UnitsPerETL/4, 3)
	id := terms.ID()
	n.next(t, n.open(t, terms))
	b := n.next(t, n.op(t, EscrowRelease, id, 0, SignerSeller))
	closed := dayOf(b.Header.Timestamp)
	n.d = int(closed-dayOf(day(0))) + EscrowRetentionDays - 2
	n.next(t)
	if _, ok := n.st.Escrow(id); !ok {
		t.Fatal("élagué trop tôt")
	}
	n.next(t)
	if _, ok := n.st.Escrow(id); ok {
		t.Fatal("non élagué après 90 jours")
	}
	if n.st.Reputation(n.buyer.addr).Completed != 1 {
		t.Fatal("réputation perdue à l'élagage")
	}
}

// Le proposant écarte les opérations inapplicables, garde l'ordre, et le bloc obtenu est valide.
func TestFilterEscrowOps(t *testing.T) {
	n := newEscrowNet(t)
	terms := n.terms(true, UnitsPerETL/2, 3)
	id := terms.ID()
	ops := []EscrowOp{
		n.op(t, EscrowPaid, id, 0, SignerBuyer), // avant l'ouverture : écarté
		n.open(t, terms),
		n.open(t, terms), // doublon : écarté
		n.op(t, EscrowPaid, id, 0, SignerBuyer),
		n.op(t, EscrowDispute, id, 1, SignerSeller),
		n.open(t, n.terms(false, UnitsPerETL, 3)), // solde insuffisant après la première : écarté
	}
	// Une transaction du vendeur dans le même bloc compte avant les opérations.
	tx := makeTx(t, n.p, n.seller, n.arbiter.addr, UnitsPerETL/4, 0, day(1))
	got := n.st.FilterEscrowOps([]Transaction{tx}, ops, day(2), 100, n.p)
	if len(got) != 3 || got[0].Kind != EscrowOpen || got[1].Kind != EscrowPaid || got[2].Kind != EscrowDispute {
		t.Fatalf("%d opérations retenues : %+v", len(got), got)
	}
	if len(n.st.FilterEscrowOps([]Transaction{tx}, ops, day(2), 2, n.p)) != 2 {
		t.Fatal("maximum non respecté")
	}
	// Seule la transaction du même bloc rend l'ouverture impossible : elle doit être comptée.
	big := makeTx(t, n.p, n.seller, n.arbiter.addr, UnitsPerETL*6/10, 0, day(1))
	if len(n.st.FilterEscrowOps(nil, ops[1:2], day(2), 10, n.p)) != 1 || len(n.st.FilterEscrowOps([]Transaction{big}, ops[1:2], day(2), 10, n.p)) != 0 {
		t.Fatal("transactions du bloc non prises en compte")
	}
	n.d++
	n.produce(t, n.d, n.founders, func(b *Block) { b.Txs = []Transaction{tx}; b.EscrowOps = got })
	if err := n.st.CheckEscrowOp(&ops[1], day(3), n.p); err != ErrEscrowUnknown {
		t.Fatalf("ouverture déjà appliquée : %v", err)
	}
}

// Une opération d'escrow compte comme une transaction dans la limite du bloc.
func TestEscrowCountsTowardBlockLimit(t *testing.T) {
	n := newEscrowNet(t)
	p := n.p
	p.MaxBlockTxs = 1
	b := n.build(t, 2, n.founders, func(b *Block) {
		b.Txs = []Transaction{makeTx(t, n.p, n.seller, n.arbiter.addr, UnitsPerETL/4, 0, day(1))}
		b.EscrowOps = []EscrowOp{n.open(t, n.terms(false, UnitsPerETL/4, 3))}
	})
	if err := ValidateBlock(&b, &n.tip, p, n.st.IsAdmitted); err != ErrBlockTooManyTxs {
		t.Fatalf("err = %v", err)
	}
	b.Header.EscrowRoot[0] ^= 1
	if err := ValidateBlock(&b, &n.tip, n.p, n.st.IsAdmitted); err != ErrBlockEscrowRoot {
		t.Fatalf("racine : err = %v", err)
	}
}

func TestEscrowCodec(t *testing.T) {
	n := newEscrowNet(t)
	terms := n.terms(true, UnitsPerETL/2, 3)
	ops := []EscrowOp{n.open(t, terms), n.op(t, EscrowRelease, terms.ID(), 7, SignerBuyer, SignerArbiter)}
	for _, op := range ops {
		enc := EncodeEscrowOp(&op)
		got, err := DecodeEscrowOp(enc, n.p.AddressHRP)
		if err != nil || !bytes.Equal(EncodeEscrowOp(&got), enc) || got.ID() != op.ID() || got.VerifySignatures(n.p) != nil {
			t.Fatalf("aller-retour : %v", err)
		}
		for i := range enc {
			if _, err := DecodeEscrowOp(enc[:i], n.p.AddressHRP); err == nil {
				t.Fatalf("tronqué à %d octets : accepté", i)
			}
		}
	}
	for name, raw := range map[string][]byte{
		"type inconnu":      {9},
		"aucun signataire":  append(append([]byte{byte(EscrowPaid)}, make([]byte, 36)...), 0),
		"masque hors plage": append(append([]byte{byte(EscrowPaid)}, make([]byte, 36)...), 8),
	} {
		if _, err := DecodeEscrowOp(raw, n.p.AddressHRP); err == nil {
			t.Errorf("%s : accepté", name)
		}
	}
	e := Escrow{Terms: terms, Deadline: 5, Seq: 2, Paid: true, Disputed: true, Outcome: EscrowReleased, ClosedDay: 9}
	got, err := DecodeEscrow(AppendEscrow(nil, e), n.p.AddressHRP)
	if err != nil || !bytes.Equal(AppendEscrow(nil, got), AppendEscrow(nil, e)) {
		t.Fatalf("échange : %v", err)
	}
	bad := AppendEscrow(nil, e)
	bad[len(bad)-9] = 3
	if _, err := DecodeEscrow(bad, n.p.AddressHRP); err == nil {
		t.Fatal("issue inconnue acceptée")
	}
	r := Reputation{1, 2, 3, 4}
	if got, err := DecodeReputation(AppendReputation(nil, r)); err != nil || got != r {
		t.Fatalf("réputation : %v", err)
	}
	op := ops[1]
	// Objets construits en mémoire (le décodeur les refuse déjà) : refusés aussi par la vérification.
	weird := ops[1]
	weird.Kind = 9
	if weird.VerifySignatures(n.p) != ErrEscrowState {
		t.Fatal("type inconnu accepté")
	}
	weird = ops[1]
	weird.Signers |= 8 // bit hors des rôles, avec une signature de plus pour garder le compte cohérent
	weird.Signatures = append(append([]crypto.Signature(nil), weird.Signatures...), weird.Signatures[0])
	if weird.VerifySignatures(n.p) != ErrEscrowSigners {
		t.Fatal("masque hors plage accepté")
	}
	if err := (&EscrowOp{Kind: EscrowOpen, Terms: terms}).VerifySignatures(n.p); err != ErrEscrowSigners {
		t.Fatalf("ouverture non signée : %v", err)
	}
	if err := op.Sign(n.seller.key, SignerBuyer, n.p); err != ErrEscrowSigners {
		t.Fatalf("rôle déjà signé : %v", err)
	}
}

// Fuzzing du décodage d'une opération : jamais de panique ; toute entrée acceptée se ré-encode à l'identique.
func FuzzDecodeEscrowOp(f *testing.F) {
	terms := EscrowTerms{Seller: crypto.Address{HRP: "etl", Hash: addrKey{1}}, Buyer: crypto.Address{HRP: "etl", Hash: addrKey{2}},
		Arbiter: crypto.Address{HRP: "etl", Hash: addrKey{3}}, Amount: 5, ArbiterFee: 1, Fee: 10000, Days: 3, Timestamp: 7}
	f.Add(EncodeEscrowOp(&EscrowOp{Kind: EscrowOpen, Terms: terms, Signers: 5, Signatures: make([]crypto.Signature, 2)}))
	f.Add(EncodeEscrowOp(&EscrowOp{Kind: EscrowRefund, Escrow: crypto.Hash{1}, Seq: 3, Signers: 2, Signatures: make([]crypto.Signature, 1)}))
	f.Fuzz(func(t *testing.T, data []byte) {
		op, err := DecodeEscrowOp(data, "etl")
		if err != nil {
			return
		}
		if !bytes.Equal(EncodeEscrowOp(&op), data) {
			t.Fatal("ré-encodage différent")
		}
		_ = op.VerifySignatures(testParams())
	})
}

// Le genesis ne contient aucune opération d'escrow.
func TestEscrowNotInGenesis(t *testing.T) {
	p := testParams()
	ws := newWallets(t, p, p.MinFounders)
	g, err := Genesis(p, GenesisMessage, IPCRecord{Value: 103070, Period: "2026-Q3"}, day(0), addrs(ws))
	if err != nil {
		t.Fatal(err)
	}
	g.EscrowOps = []EscrowOp{{Kind: EscrowPaid, Signers: SignerBuyer, Signatures: make([]crypto.Signature, 1)}}
	if err := NewState().ApplyBlock(&g, p); err != ErrGenesisContent {
		t.Fatalf("err = %v", err)
	}
}
