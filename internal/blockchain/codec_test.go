package blockchain

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"
)

// richBlock construit un bloc qui utilise toutes les listes : participants, transactions,
// candidatures, observations et preuve de double participation (il n'a pas besoin d'être valide).
func richBlock(t testing.TB) (Block, Block) {
	p := testParams()
	n := newNet(t, p)
	n.produce(t, 1, n.founders, nil)
	cand := newWallet(t, p)
	n.produce(t, 2, n.founders, withRegs(n.register(t, cand, n.founders[0], 2)))
	a, b := doubleParticipation(t, n, n.founders[1], 3)
	tx := makeTx(t, p, n.founders[2], n.founders[3].addr, UnitsPerETL/3, 0, day(2))
	blk := n.build(t, 3, append(append([]wallet(nil), n.founders[2:]...), cand), func(bl *Block) {
		bl.Txs = []Transaction{tx}
		bl.Equivocations = []Equivocation{NewEquivocation(a, b)}
		bl.Registrations = []Registration{n.register(t, newWallet(t, p), n.founders[4], 3)}
		withIPC(q4, ipcVotes(t, p, n.founders[:7], q4))(bl)
		bl.Exclusions = []Exclusion{exclusion(t, p, n.founders[:8], n.founders[9].addr, 3)}
	})
	g, _ := Genesis(p, GenesisMessage, testIPC(), day(0), addrs(n.founders))
	return blk, g
}

func TestBlockRoundTrip(t *testing.T) {
	blk, genesis := richBlock(t)
	if len(blk.Participants) == 0 || len(blk.Observations) == 0 || len(blk.Txs) == 0 ||
		len(blk.Registrations) == 0 || len(blk.Equivocations) == 0 || len(genesis.Founders) == 0 ||
		len(blk.IPCVotes) == 0 || len(blk.Exclusions) == 0 || blk.Header.IPC == nil {
		t.Fatal("prémisse : toutes les listes doivent être remplies")
	}
	for name, b := range map[string]Block{"bloc": blk, "genesis": genesis} {
		enc := EncodeBlock(&b)
		got, err := DecodeBlock(enc, "etl")
		if err != nil {
			t.Fatalf("%s : %v", name, err)
		}
		if !reflect.DeepEqual(got, b) {
			t.Fatalf("%s : aller-retour différent", name)
		}
		if !bytes.Equal(EncodeBlock(&got), enc) {
			t.Fatalf("%s : ré-encodage différent", name)
		}
		if got.Header.Hash() != b.Header.Hash() {
			t.Fatalf("%s : hash différent après décodage", name)
		}
	}
}

func TestDecodeRejectsTruncatedAndTrailing(t *testing.T) {
	blk, _ := richBlock(t)
	enc := EncodeBlock(&blk)
	for i := 0; i < len(enc); i++ {
		if _, err := DecodeBlock(enc[:i], "etl"); err == nil {
			t.Fatalf("bloc tronqué à %d/%d octets accepté", i, len(enc))
		}
	}
	if _, err := DecodeBlock(append(enc, 0), "etl"); err == nil {
		t.Fatal("octet en trop accepté")
	}
	tx := EncodeTransaction(&blk.Txs[0])
	for i := 0; i < len(tx); i++ {
		if _, err := DecodeTransaction(tx[:i], "etl"); err == nil {
			t.Fatalf("transaction tronquée à %d octets acceptée", i)
		}
	}
}

func TestDecodeRejectsNonCanonical(t *testing.T) {
	blk, _ := richBlock(t)
	enc := EncodeBlock(&blk)

	badVersion := append([]byte(nil), enc...)
	badVersion[0] = BlockVersion + 1
	if _, err := DecodeBlock(badVersion, "etl"); err == nil {
		t.Error("version de bloc inconnue acceptée")
	}

	// Drapeau IPC = 2 (seuls 0 et 1 sont canoniques).
	h := blk.Header
	h.IPC = nil
	hEnc := h.Encode()
	ipcFlag := len(hEnc) - 20 - 1
	hEnc[ipcFlag] = 2
	if _, err := DecodeHeader(hEnc, "etl"); err == nil {
		t.Error("booléen non canonique accepté")
	}

	tx := EncodeTransaction(&blk.Txs[0])
	tx[0] = TxVersion + 1
	if _, err := DecodeTransaction(tx, "etl"); err == nil {
		t.Error("version de transaction inconnue acceptée")
	}

	node := AppendNode(nil, Node{Status: StatusAdmitted})
	node[0] = byte(StatusExcluded) + 1
	if _, err := DecodeNode(node); err == nil {
		t.Error("statut de nœud inconnu accepté")
	}
}

// Une liste annonçant 4 milliards d'éléments ne doit provoquer aucune allocation.
func TestDecodeRejectsHugeCount(t *testing.T) {
	blk, _ := richBlock(t)
	blk = Block{Header: blk.Header, Signature: blk.Signature} // toutes les listes vides
	enc := EncodeBlock(&blk)
	countAt := len(blk.Header.Encode()) + len(blk.Signature) // premier compteur : participants
	binary.BigEndian.PutUint32(enc[countAt:], 0xffffffff)
	allocs := testing.AllocsPerRun(10, func() { _, _ = DecodeBlock(enc, "etl") })
	if _, err := DecodeBlock(enc, "etl"); err == nil {
		t.Fatal("compteur démesuré accepté")
	}
	if allocs > 10 {
		t.Fatalf("%v allocations pour une entrée hostile", allocs)
	}
}

func TestAccountNodeRoundTrip(t *testing.T) {
	a := Account{Balance: 123456789, Nonce: 42}
	if got, err := DecodeAccount(AppendAccount(nil, a)); err != nil || got != a {
		t.Fatalf("compte : %v, %v", got, err)
	}
	n := Node{Status: StatusCandidate, Sponsor: addrKey{7}, Founder: true, CandidateDay: -3, ObservedDays: 5,
		Reactivating: true, AdmittedDay: 20000, LastParticipationDay: 20001, HasSponsored: true,
		LastSponsorDay: 19990, SponsorBanUntil: 20365}
	enc := AppendNode(nil, n)
	if len(enc) != nodeSize {
		t.Fatalf("taille = %d, attendu %d", len(enc), nodeSize)
	}
	if got, err := DecodeNode(enc); err != nil || got != n {
		t.Fatalf("nœud : %+v, %v", got, err)
	}
}

// --- Fuzzing : toute entrée acceptée doit se ré-encoder à l'identique ---
// Lancer : go test ./internal/blockchain -run XXX -fuzz FuzzDecodeBlock -fuzztime 30s

func FuzzDecodeBlock(f *testing.F) {
	blk, g := richBlock(f)
	f.Add(EncodeBlock(&blk))
	f.Add(EncodeBlock(&g))
	f.Fuzz(func(t *testing.T, in []byte) {
		b, err := DecodeBlock(in, "etl")
		if err != nil {
			return
		}
		if !bytes.Equal(EncodeBlock(&b), in) {
			t.Fatal("encodage non canonique accepté")
		}
	})
}

func FuzzDecodeTransaction(f *testing.F) {
	blk, _ := richBlock(f)
	f.Add(EncodeTransaction(&blk.Txs[0]))
	f.Fuzz(func(t *testing.T, in []byte) {
		tx, err := DecodeTransaction(in, "etl")
		if err != nil {
			return
		}
		if !bytes.Equal(EncodeTransaction(&tx), in) {
			t.Fatal("encodage non canonique accepté")
		}
	})
}

func FuzzDecodeNode(f *testing.F) {
	f.Add(AppendNode(nil, Node{Status: StatusAdmitted, Founder: true}))
	f.Fuzz(func(t *testing.T, in []byte) {
		n, err := DecodeNode(in)
		if err != nil {
			return
		}
		if !bytes.Equal(AppendNode(nil, n), in) {
			t.Fatal("encodage non canonique accepté")
		}
	})
}
