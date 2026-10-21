package escrow

import (
	"strings"
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/blockchain/chaintest"
	"github.com/makoto-kanamoto/etl/internal/crypto"
)

type parties struct {
	p                      blockchain.Params
	seller, buyer, arbiter chaintest.Wallet
}

func newParties(t *testing.T) parties {
	p := chaintest.Params()
	return parties{p: p, seller: chaintest.NewWallet(t, p), buyer: chaintest.NewWallet(t, p), arbiter: chaintest.NewWallet(t, p)}
}

func (x parties) key(role uint8) *crypto.PrivateKey {
	return map[uint8]chaintest.Wallet{blockchain.SignerSeller: x.seller, blockchain.SignerBuyer: x.buyer, blockchain.SignerArbiter: x.arbiter}[role].Key
}

func (x parties) terms(t *testing.T, withArbiter bool) blockchain.EscrowTerms {
	t.Helper()
	arb, fee := crypto.Address{}, uint64(0)
	if withArbiter {
		arb, fee = x.arbiter.Addr, 5000
	}
	terms, err := NewTerms(x.seller.Addr, x.buyer.Addr, arb, blockchain.UnitsPerETL/2, fee, 3, chaintest.Day(1), x.p)
	if err != nil {
		t.Fatal(err)
	}
	return terms
}

func TestNewTerms(t *testing.T) {
	x := newParties(t)
	terms := x.terms(t, true)
	if terms.Fee != blockchain.UnitsPerETL/2*10/10000 {
		t.Fatalf("frais : %d", terms.Fee)
	}
	none := crypto.Address{}
	other := chaintest.NewWallet(t, blockchain.Params{AddressHRP: "etlde"}).Addr
	for name, args := range map[string]struct {
		seller, buyer, arbiter crypto.Address
		amount, arbFee         uint64
		days                   uint8
	}{
		"montant nul":                {x.seller.Addr, x.buyer.Addr, none, 0, 0, 3},
		"vendeur = acheteur":         {x.seller.Addr, x.seller.Addr, none, 1000, 0, 3},
		"délai nul":                  {x.seller.Addr, x.buyer.Addr, none, 1000, 0, 0},
		"délai de 31 jours":          {x.seller.Addr, x.buyer.Addr, none, 1000, 0, 31},
		"rémunération sans arbitre":  {x.seller.Addr, x.buyer.Addr, none, 1000, 1, 3},
		"arbitre = vendeur":          {x.seller.Addr, x.buyer.Addr, x.seller.Addr, 1000, 0, 3},
		"arbitre = acheteur":         {x.seller.Addr, x.buyer.Addr, x.buyer.Addr, 1000, 0, 3},
		"acheteur d'un autre réseau": {x.seller.Addr, other, none, 1000, 0, 3},
		"arbitre d'un autre réseau":  {x.seller.Addr, x.buyer.Addr, other, 1000, 0, 3},
	} {
		if _, err := NewTerms(args.seller, args.buyer, args.arbiter, args.amount, args.arbFee, args.days, chaintest.Day(1), x.p); err != ErrTerms {
			t.Errorf("%s : err = %v", name, err)
		}
	}
}

// Les opérations construites par ces outils sont acceptées par le consensus.
func TestOpenCosignActAccepted(t *testing.T) {
	net := chaintest.New(t, chaintest.Params())
	net.Next(t, 1, nil) // chaque fondateur : 1 ETL
	x := parties{p: net.P, seller: net.Founders[0], arbiter: net.Founders[1], buyer: chaintest.NewWallet(t, net.P)}
	terms := x.terms(t, true)
	op, err := Open(x.seller.Key, terms, x.p)
	if err != nil {
		t.Fatal(err)
	}
	if err := net.State.CheckEscrowOp(&op, chaintest.Day(2), x.p); err != blockchain.ErrEscrowSigners {
		t.Fatalf("sans cosignature : err = %v", err)
	}
	// Transmission à l'arbitre sous forme de texte.
	got, err := DecodeText(EncodeText(&op), x.p.AddressHRP)
	if err != nil {
		t.Fatal(err)
	}
	if err := Cosign(x.arbiter.Key, &got, time.Unix(chaintest.Day(1), 0).Add(time.Hour), x.p); err != nil {
		t.Fatal(err)
	}
	if err := net.State.CheckEscrowOp(&got, chaintest.Day(2), x.p); err != nil {
		t.Fatalf("ouverture cosignée refusée : %v", err)
	}
	net.Next(t, 2, func(b *blockchain.Block) { b.EscrowOps = []blockchain.EscrowOp{got} })
	id := terms.ID()
	e, _ := net.State.Escrow(id)
	// L'acheteur prépare la libération, l'arbitre la complète.
	rel, err := Act(x.buyer.Key, blockchain.EscrowRelease, id, e.Seq, blockchain.SignerBuyer, x.p)
	if err != nil {
		t.Fatal(err)
	}
	if err := AddSignature(x.arbiter.Key, &rel, blockchain.SignerArbiter, id, e, x.p); err != nil {
		t.Fatal(err)
	}
	if err := net.State.CheckEscrowOp(&rel, chaintest.Day(3), x.p); err != nil {
		t.Fatalf("libération refusée : %v", err)
	}
}

func TestOpenRejects(t *testing.T) {
	x := newParties(t)
	if _, err := Open(x.buyer.Key, x.terms(t, false), x.p); err != ErrRole {
		t.Fatalf("ouverture par un autre que le vendeur : %v", err)
	}
	bad := x.terms(t, false)
	bad.Fee++
	if _, err := Open(x.seller.Key, bad, x.p); err != ErrTerms {
		t.Fatalf("frais inexacts : %v", err)
	}
}

func TestCosignRejects(t *testing.T) {
	x := newParties(t)
	now := time.Unix(chaintest.Day(1), 0).Add(time.Hour)
	open := func(withArbiter bool) blockchain.EscrowOp {
		op, err := Open(x.seller.Key, x.terms(t, withArbiter), x.p)
		if err != nil {
			t.Fatal(err)
		}
		return op
	}
	forged := open(true)
	forged.Signers, forged.Signatures = 0, nil
	_ = forged.Sign(x.buyer.Key, blockchain.SignerSeller, x.p) // « vendeur » usurpé
	unsigned := blockchain.EscrowOp{Kind: blockchain.EscrowOpen, Terms: x.terms(t, true)}
	twice := open(true)
	_ = twice.Sign(x.arbiter.Key, blockchain.SignerArbiter, x.p)
	wrongFee := open(true)
	wrongFee.Terms.Fee++
	paid, _ := Act(x.buyer.Key, blockchain.EscrowPaid, crypto.Hash{1}, 0, blockchain.SignerBuyer, x.p)
	for name, c := range map[string]struct {
		op   blockchain.EscrowOp
		key  *crypto.PrivateKey
		now  time.Time
		want error
	}{
		"pas l'arbitre":       {open(true), x.buyer.Key, now, ErrRole},
		"mode simple":         {open(false), x.arbiter.Key, now, ErrRole},
		"périmée (7 jours)":   {open(true), x.arbiter.Key, now.Add(7 * 24 * time.Hour), ErrExpired},
		"datée dans le futur": {open(true), x.arbiter.Key, now.Add(-2 * time.Hour), ErrExpired},
		"vendeur usurpé":      {forged, x.arbiter.Key, now, ErrUnsigned},
		"non signée":          {unsigned, x.arbiter.Key, now, ErrUnsigned},
		"déjà cosignée":       {twice, x.arbiter.Key, now, ErrUnsigned},
		"conditions altérées": {wrongFee, x.arbiter.Key, now, ErrTerms},
		"pas une ouverture":   {paid, x.arbiter.Key, now, ErrNotOpen},
	} {
		op := c.op
		if err := Cosign(c.key, &op, c.now, x.p); err != c.want {
			t.Errorf("%s : err = %v, attendu %v", name, err, c.want)
		}
	}
	// Signée le jour D (23:00) : incluse au plus tard par le bloc du jour D+6. Cosigner le jour D+6
	// à 22:00 est encore utile ; à 23:30, le prochain bloc est celui de D+7 : trop tard.
	signed := time.Unix(chaintest.Day(1), 0)
	op := open(true)
	if err := Cosign(x.arbiter.Key, &op, signed.Add(6*24*time.Hour-time.Hour), x.p); err != nil {
		t.Fatalf("jour D+6 à 22:00 : %v", err)
	}
	op = open(true)
	if err := Cosign(x.arbiter.Key, &op, signed.Add(6*24*time.Hour+30*time.Minute), x.p); err != ErrExpired {
		t.Fatalf("jour D+6 à 23:30 : %v", err)
	}
}

func TestActAndAddSignatureRejects(t *testing.T) {
	x := newParties(t)
	if _, err := Act(x.buyer.Key, blockchain.EscrowOpen, crypto.Hash{}, 0, blockchain.SignerBuyer, x.p); err != ErrKind {
		t.Fatalf("Act ouverture : %v", err)
	}
	if _, err := Act(x.buyer.Key, 9, crypto.Hash{}, 0, blockchain.SignerBuyer, x.p); err != ErrKind {
		t.Fatalf("Act type inconnu : %v", err)
	}
	terms := x.terms(t, true)
	id := terms.ID()
	e := blockchain.Escrow{Terms: terms, Deadline: 100, Seq: 1}
	op, _ := Act(x.buyer.Key, blockchain.EscrowRelease, id, 1, blockchain.SignerBuyer, x.p)
	for name, c := range map[string]struct {
		key  *crypto.PrivateKey
		role uint8
		id   crypto.Hash
		e    blockchain.Escrow
		want error
	}{
		"clé du vendeur pour l'arbitre": {x.seller.Key, blockchain.SignerArbiter, id, e, ErrRole},
		"autre échange":                 {x.arbiter.Key, blockchain.SignerArbiter, crypto.Hash{2}, e, ErrTerms},
		"séquence dépassée":             {x.arbiter.Key, blockchain.SignerArbiter, id, blockchain.Escrow{Terms: terms, Seq: 2}, ErrTerms},
		"pas d'arbitre":                 {x.arbiter.Key, blockchain.SignerArbiter, id, blockchain.Escrow{Terms: x.terms(t, false), Seq: 1}, ErrRole},
	} {
		o := op
		o.Signatures = append([]crypto.Signature(nil), op.Signatures...)
		if err := AddSignature(c.key, &o, c.role, c.id, c.e, x.p); err != c.want {
			t.Errorf("%s : err = %v, attendu %v", name, err, c.want)
		}
	}
	open, _ := Open(x.seller.Key, terms, x.p)
	if err := AddSignature(x.arbiter.Key, &open, blockchain.SignerArbiter, id, e, x.p); err != ErrTerms {
		t.Fatalf("ouverture via AddSignature : %v", err)
	}
}

// Required dit exactement ce que le consensus accepte : pour chaque état d'échange, chaque type
// d'opération et chaque combinaison de signataires.
func TestRequiredMatchesConsensus(t *testing.T) {
	x := newParties(t)
	const day = int64(20000)
	blockTime := day*86400 + 82800 // 23:00 UTC
	checked := 0
	for _, withArb := range []bool{false, true} {
		for _, excluded := range []bool{false, true} {
			if excluded && !withArb {
				continue
			}
			for _, paid := range []bool{false, true} {
				for _, disputed := range []bool{false, true} {
					for _, deadline := range []int64{day - 1, day, day + 1} {
						for _, outcome := range []blockchain.EscrowOutcome{blockchain.EscrowPending, blockchain.EscrowReleased} {
							terms := x.terms(t, withArb)
							id := terms.ID()
							e := blockchain.Escrow{Terms: terms, Deadline: deadline, Seq: 4, Paid: paid, Disputed: disputed, Outcome: outcome}
							status := blockchain.StatusAdmitted
							if excluded {
								status = blockchain.StatusExcluded
							}
							st := blockchain.NewStateFrom(nil, map[[crypto.AddressHashSize]byte]blockchain.Node{x.arbiter.Addr.Hash: {Status: status}},
								blockchain.IPCRecord{}, blockchain.IPCRecord{}, nil, nil,
								blockchain.EscrowData{Escrows: map[crypto.Hash]blockchain.Escrow{id: e}})
							for kind := blockchain.EscrowPaid; kind <= blockchain.EscrowRefund; kind++ {
								req := Required(e, kind, day, !excluded)
								for mask := uint8(1); mask <= 7; mask++ {
									op := blockchain.EscrowOp{Kind: kind, Escrow: id, Seq: 4}
									for _, r := range []uint8{1, 2, 4} {
										if mask&r != 0 {
											_ = op.Sign(x.key(r), r, x.p)
										}
									}
									accepted := st.CheckEscrowOp(&op, blockTime, x.p) == nil
									listed := false
									for _, m := range req {
										listed = listed || m == mask
									}
									if accepted != listed {
										t.Fatalf("arbitre %v exclu %v payé %v litige %v échéance %+d issue %d type %d signataires %03b : consensus %v, Required %v",
											withArb, excluded, paid, disputed, deadline-day, outcome, kind, mask, accepted, listed)
									}
									checked++
								}
							}
						}
					}
				}
			}
		}
	}
	if checked != 3*2*2*3*2*4*7 {
		t.Fatalf("%d combinaisons vérifiées", checked)
	}
}

func TestTextRejects(t *testing.T) {
	x := newParties(t)
	op, _ := Open(x.seller.Key, x.terms(t, false), x.p)
	text := EncodeText(&op)
	if got, err := DecodeText("  "+text+"\n", x.p.AddressHRP); err != nil || got.ID() != op.ID() {
		t.Fatalf("espaces autour : %v", err)
	}
	for name, s := range map[string]string{
		"sans préfixe":       strings.TrimPrefix(text, TextPrefix),
		"autre préfixe":      "etl-tx:" + strings.TrimPrefix(text, TextPrefix),
		"remplissage":        text + "=",
		"caractère invalide": text[:20] + "+" + text[21:],
		"tronqué":            text[:len(text)-4],
		"vide":               TextPrefix,
		"remplissage non nul": func() string { // bit de poids faible du dernier caractère : non canonique
			const b64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
			return text[:len(text)-1] + string(b64[strings.IndexByte(b64, text[len(text)-1])+1])
		}(),
	} {
		if _, err := DecodeText(s, x.p.AddressHRP); err != ErrText {
			t.Errorf("%s : err = %v", name, err)
		}
	}
}

// Fuzzing : jamais de panique ; tout texte accepté se ré-encode à l'identique.
func FuzzDecodeText(f *testing.F) {
	f.Add(TextPrefix + "AQ")
	f.Add("etl-escrow:Aw")
	f.Fuzz(func(t *testing.T, s string) {
		op, err := DecodeText(s, "etl")
		if err != nil {
			return
		}
		if EncodeText(&op) != strings.TrimSpace(s) {
			t.Fatal("ré-encodage différent")
		}
	})
}
