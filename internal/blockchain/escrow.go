package blockchain

import (
	"encoding/binary"
	"errors"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// Escrow : échange pair à pair euros ↔ ETL. Les euros circulent hors chaîne ; la chaîne bloque les
// ETL du vendeur jusqu'à ce que l'échange soit tranché.
//
//	Ouverture   signée par le vendeur (et cosignée par l'arbitre s'il y en a un) : le vendeur verse
//	            montant + rémunération de l'arbitre + frais d'un transfert (au proposant du bloc).
//	« Payé »    signé par l'acheteur, une fois, avant l'échéance.
//	Litige      signé par le vendeur ou l'acheteur, une fois par échange : alerte publique.
//	Libération  vers l'acheteur : le vendeur seul, ou l'acheteur avec l'arbitre (2 sur 3).
//	Rembourse-  vers le vendeur : l'acheteur seul, ou le vendeur avec l'arbitre (2 sur 3), ou le
//	ment        vendeur seul si l'échéance est passée sans « payé ».
//
// Sans arbitre (mode simple), après « payé » seuls la libération par le vendeur ou le remboursement
// accordé par l'acheteur restent possibles. La rémunération de l'arbitre ne lui est versée que s'il
// cosigne le règlement ; sinon elle revient au vendeur. Un arbitre exclu du registre perd son pouvoir.
//
// Les opérations après l'ouverture sont gratuites (l'acheteur, souvent nouveau venu, n'a pas d'ETL) :
// au plus un « payé », un litige et un règlement par échange, ce qui borne le spam au coût de
// l'ouverture. Chaque opération porte la séquence de l'échange (nombre d'opérations déjà appliquées),
// pas le nonce d'un compte : un acheteur sans compte peut agir, et une opération ne se rejoue pas.

const (
	EscrowMinDays          = 1  // délai choisi par le vendeur : bornes fixes
	EscrowMaxDays          = 30 //
	EscrowOpenValidityDays = 7  // une ouverture signée n'est valable que 7 jours (anti-rejeu)
	EscrowRetentionDays    = 90 // un échange clos est élagué 90 jours après ; la réputation reste
)

// EscrowKind est le type d'une opération d'escrow.
type EscrowKind uint8

const (
	EscrowOpen EscrowKind = iota + 1
	EscrowPaid
	EscrowDispute
	EscrowRelease
	EscrowRefund
)

// Signataires d'une opération (masque).
const (
	SignerSeller  uint8 = 1 << iota // vendeur
	SignerBuyer                     // acheteur
	SignerArbiter                   // arbitre
)

var (
	ErrEscrowTerms     = errors.New("blockchain: conditions d'escrow invalides")
	ErrEscrowUnknown   = errors.New("blockchain: escrow inconnu ou déjà ouvert")
	ErrEscrowClosed    = errors.New("blockchain: escrow déjà réglé")
	ErrEscrowSeq       = errors.New("blockchain: séquence d'escrow incorrecte")
	ErrEscrowSigners   = errors.New("blockchain: signataires d'escrow non autorisés")
	ErrEscrowSignature = errors.New("blockchain: signature d'escrow invalide")
	ErrEscrowState     = errors.New("blockchain: opération d'escrow impossible dans cet état")
	ErrEscrowExpired   = errors.New("blockchain: ouverture d'escrow périmée ou postérieure au bloc")
)

// EscrowTerms sont les conditions fixées à l'ouverture.
type EscrowTerms struct {
	Seller     crypto.Address
	Buyer      crypto.Address
	Arbiter    crypto.Address // adresse nulle : mode simple, sans arbitre
	Amount     uint64         // ETL bloqués pour l'acheteur, en unités
	ArbiterFee uint64         // rémunération de l'arbitre s'il intervient (0 sans arbitre)
	Fee        uint64         // frais d'un transfert du montant, au proposant du bloc d'ouverture
	Days       uint8          // délai en jours, dans [EscrowMinDays ; EscrowMaxDays]
	Timestamp  int64          // heure Unix de la signature
}

// HasArbiter indique si l'échange a un arbitre.
func (t *EscrowTerms) HasArbiter() bool { return t.Arbiter.Hash != [crypto.AddressHashSize]byte{} }

// ID renvoie l'identifiant de l'échange : Blake3 de ses conditions.
func (t *EscrowTerms) ID() crypto.Hash {
	return crypto.Sum(crypto.ContextEscrow, appendEscrowTerms([]byte{'T'}, t))
}

// EscrowOp est une opération sur un échange. Terms ne sert qu'à l'ouverture ; Escrow et Seq aux autres.
type EscrowOp struct {
	Kind       EscrowKind
	Terms      EscrowTerms
	Escrow     crypto.Hash
	Seq        uint32
	Signers    uint8 // masque SignerSeller | SignerBuyer | SignerArbiter
	Signatures []crypto.Signature
}

// message renvoie ce que signent tous les signataires de l'opération.
func (op *EscrowOp) message() []byte {
	b := []byte{byte(op.Kind)}
	if op.Kind == EscrowOpen {
		return appendEscrowTerms(b, &op.Terms)
	}
	b = append(b, op.Escrow[:]...)
	return binary.BigEndian.AppendUint32(b, op.Seq)
}

// ID renvoie l'identifiant de l'opération (signatures comprises).
func (op *EscrowOp) ID() crypto.Hash {
	return crypto.Sum(crypto.ContextEscrow, appendEscrowOp([]byte{'O'}, op))
}

// Sign ajoute la signature de key pour le rôle signer. Les signatures sont rangées dans l'ordre
// des rôles (vendeur, acheteur, arbitre) : un seul encodage possible.
func (op *EscrowOp) Sign(key *crypto.PrivateKey, signer uint8, p Params) error {
	if signer != SignerSeller && signer != SignerBuyer && signer != SignerArbiter || op.Signers&signer != 0 {
		return ErrEscrowSigners
	}
	sig, err := key.Sign(p.ChainID, crypto.PurposeEscrow, op.message())
	if err != nil {
		return err
	}
	var sigs []crypto.Signature
	j := 0
	for _, role := range []uint8{SignerSeller, SignerBuyer, SignerArbiter} {
		switch {
		case role == signer:
			sigs = append(sigs, sig)
		case op.Signers&role != 0:
			sigs = append(sigs, op.Signatures[j])
			j++
		}
	}
	op.Signers |= signer
	op.Signatures = sigs
	return nil
}

// signerAddrs recouvre l'adresse de chaque signataire, par rôle (vérification sans contexte).
func (op *EscrowOp) signerAddrs(p Params) (map[uint8]crypto.Address, error) {
	if op.Signers == 0 || op.Signers > SignerSeller|SignerBuyer|SignerArbiter || len(op.Signatures) != onesCount(op.Signers) {
		return nil, ErrEscrowSigners
	}
	out := map[uint8]crypto.Address{}
	j := 0
	msg := op.message()
	for _, role := range []uint8{SignerSeller, SignerBuyer, SignerArbiter} {
		if op.Signers&role == 0 {
			continue
		}
		pub, err := crypto.RecoverPublicKey(p.ChainID, crypto.PurposeEscrow, msg, op.Signatures[j])
		if err != nil {
			return nil, ErrEscrowSignature
		}
		out[role] = pub.Address(p.AddressHRP)
		j++
	}
	return out, nil
}

// VerifySignatures contrôle ce qui ne dépend pas de l'état : type, forme, signatures récupérables
// et, pour une ouverture, signataires conformes aux conditions.
func (op *EscrowOp) VerifySignatures(p Params) error {
	if op.Kind < EscrowOpen || op.Kind > EscrowRefund {
		return ErrEscrowState
	}
	signers, err := op.signerAddrs(p)
	if err != nil {
		return err
	}
	if op.Kind == EscrowOpen {
		return op.Terms.checkSigners(signers)
	}
	return nil
}

func (t *EscrowTerms) checkSigners(signers map[uint8]crypto.Address) error {
	want := SignerSeller
	if t.HasArbiter() {
		want |= SignerArbiter
	}
	if mask(signers) != want || signers[SignerSeller] != t.Seller || (t.HasArbiter() && signers[SignerArbiter] != t.Arbiter) {
		return ErrEscrowSigners
	}
	return nil
}

func mask(signers map[uint8]crypto.Address) uint8 {
	var m uint8
	for role := range signers {
		m |= role
	}
	return m
}

func onesCount(m uint8) int {
	n := 0
	for ; m != 0; m &= m - 1 {
		n++
	}
	return n
}

// EscrowOutcome est l'issue d'un échange.
type EscrowOutcome uint8

const (
	EscrowPending  EscrowOutcome = iota // en cours
	EscrowReleased                      // ETL versés à l'acheteur
	EscrowRefunded                      // ETL rendus au vendeur
)

// Escrow est l'état d'un échange dans la chaîne.
type Escrow struct {
	Terms     EscrowTerms
	Deadline  int64 // jour (heure Unix / 86400) à partir duquel l'échéance est passée
	Seq       uint32
	Paid      bool
	Disputed  bool
	Outcome   EscrowOutcome
	ClosedDay int64 // jour du règlement (élagage)
}

// Reputation compte, par adresse, les échanges et litiges (vendeur comme acheteur).
type Reputation struct {
	Completed      uint64 // échanges menés à terme (ETL versés à l'acheteur)
	Refunded       uint64 // échanges remboursés au vendeur
	DisputesOpened uint64 // litiges ouverts par cette adresse
	DisputesLost   uint64 // règlements cosignés par l'arbitre en défaveur de cette adresse
}

// escrowView applique des opérations à une copie de l'état des échanges et des comptes.
type escrowView struct {
	st      *State
	day     int64
	time    int64
	p       Params
	get     func(addrKey) Account
	set     func(addrKey, Account)
	escrows map[crypto.Hash]Escrow
	reps    map[addrKey]Reputation
	fees    uint64
}

func (v *escrowView) escrow(id crypto.Hash) (Escrow, bool) {
	if e, ok := v.escrows[id]; ok {
		return e, true
	}
	e, ok := v.st.escrows[id]
	return e, ok
}

func (v *escrowView) rep(a crypto.Address) Reputation {
	if r, ok := v.reps[a.Hash]; ok {
		return r
	}
	return v.st.reps[a.Hash]
}

func (v *escrowView) bumpRep(a crypto.Address, f func(*Reputation)) {
	r := v.rep(a)
	f(&r)
	v.reps[a.Hash] = r
}

func (v *escrowView) credit(a crypto.Address, amount uint64) error {
	if amount == 0 {
		return nil
	}
	acc := v.get(a.Hash)
	var err error
	if acc.Balance, err = checkedAdd(acc.Balance, amount); err != nil {
		return err
	}
	v.set(a.Hash, acc)
	return nil
}

// apply applique une opération, ou renvoie une erreur sans rien modifier.
func (v *escrowView) apply(op *EscrowOp) error {
	signers, err := op.signerAddrs(v.p)
	if err != nil {
		return err
	}
	if op.Kind == EscrowOpen {
		return v.open(op, signers)
	}
	e, ok := v.escrow(op.Escrow)
	switch {
	case !ok:
		return ErrEscrowUnknown
	case e.Outcome != EscrowPending:
		return ErrEscrowClosed
	case op.Seq != e.Seq:
		return ErrEscrowSeq
	}
	t := &e.Terms
	is := func(want uint8) bool { // signataires exactement want, aux bonnes adresses
		if mask(signers) != want {
			return false
		}
		for role, a := range signers {
			if a != map[uint8]crypto.Address{SignerSeller: t.Seller, SignerBuyer: t.Buyer, SignerArbiter: t.Arbiter}[role] {
				return false
			}
		}
		if want&SignerArbiter != 0 && (!t.HasArbiter() || v.st.Status(t.Arbiter) == StatusExcluded) {
			return false
		}
		return true
	}
	switch op.Kind {
	case EscrowPaid:
		if !is(SignerBuyer) {
			return ErrEscrowSigners
		}
		if e.Paid || v.day >= e.Deadline {
			return ErrEscrowState
		}
		e.Paid = true
	case EscrowDispute:
		if !is(SignerSeller) && !is(SignerBuyer) {
			return ErrEscrowSigners
		}
		if e.Disputed {
			return ErrEscrowState
		}
		e.Disputed = true
		v.bumpRep(signers[mask(signers)], func(r *Reputation) { r.DisputesOpened++ })
	case EscrowRelease:
		byArbiter := is(SignerBuyer | SignerArbiter)
		if !is(SignerSeller) && !byArbiter {
			return ErrEscrowSigners
		}
		if err := v.settle(&e, t.Buyer, byArbiter, t.Seller); err != nil {
			return err
		}
		e.Outcome = EscrowReleased
	case EscrowRefund:
		byArbiter := is(SignerSeller | SignerArbiter)
		lapsed := is(SignerSeller) && !e.Paid && v.day >= e.Deadline
		if !is(SignerBuyer) && !byArbiter && !lapsed {
			return ErrEscrowSigners
		}
		if err := v.settle(&e, t.Seller, byArbiter, t.Buyer); err != nil {
			return err
		}
		e.Outcome = EscrowRefunded
	default:
		return ErrEscrowState
	}
	e.Seq++
	if e.Outcome != EscrowPending {
		e.ClosedDay = v.day
		counter := func(r *Reputation) { r.Completed++ }
		if e.Outcome == EscrowRefunded {
			counter = func(r *Reputation) { r.Refunded++ }
		}
		v.bumpRep(t.Seller, counter)
		v.bumpRep(t.Buyer, counter)
	}
	v.escrows[op.Escrow] = e
	return nil
}

// settle verse le montant au bénéficiaire, et la rémunération à l'arbitre s'il a cosigné (sinon au
// vendeur). Un règlement cosigné par l'arbitre compte comme un litige perdu pour l'autre partie.
func (v *escrowView) settle(e *Escrow, to crypto.Address, byArbiter bool, loser crypto.Address) error {
	t := &e.Terms
	if err := v.credit(to, t.Amount); err != nil {
		return err
	}
	feeTo := t.Seller
	if byArbiter {
		feeTo = t.Arbiter
		v.bumpRep(loser, func(r *Reputation) { r.DisputesLost++ })
	}
	return v.credit(feeTo, t.ArbiterFee)
}

func (v *escrowView) open(op *EscrowOp, signers map[uint8]crypto.Address) error {
	t := &op.Terms
	id := t.ID()
	if err := t.checkSigners(signers); err != nil {
		return err
	}
	fee, err := RequiredFee(t.Amount, v.p)
	switch {
	case err != nil || t.Amount == 0 || t.Fee != fee || t.Seller == t.Buyer:
		return ErrEscrowTerms
	case t.Seller.HRP != v.p.AddressHRP || t.Buyer.HRP != v.p.AddressHRP:
		return ErrEscrowTerms
	case t.Days < EscrowMinDays || t.Days > EscrowMaxDays:
		return ErrEscrowTerms
	case t.HasArbiter() && (t.Arbiter == t.Seller || t.Arbiter == t.Buyer || t.Arbiter.HRP != v.p.AddressHRP || !v.st.IsAdmitted(t.Arbiter)):
		return ErrEscrowTerms
	case !t.HasArbiter() && t.ArbiterFee != 0:
		return ErrEscrowTerms
	case t.Timestamp > v.time || dayOf(t.Timestamp)+EscrowOpenValidityDays <= v.day:
		return ErrEscrowExpired
	case op.Seq != 0 || op.Escrow != (crypto.Hash{}):
		return ErrEscrowTerms
	}
	if _, exists := v.escrow(id); exists {
		return ErrEscrowUnknown
	}
	cost, err := checkedAdd(t.Amount, t.ArbiterFee)
	if err == nil {
		cost, err = checkedAdd(cost, t.Fee)
	}
	if err != nil {
		return ErrEscrowTerms
	}
	seller := v.get(t.Seller.Hash)
	if seller.Balance < cost {
		return ErrInsufficientFunds
	}
	if v.fees, err = checkedAdd(v.fees, t.Fee); err != nil {
		return err
	}
	seller.Balance -= cost
	v.set(t.Seller.Hash, seller)
	v.escrows[id] = Escrow{Terms: *t, Deadline: v.day + int64(t.Days)}
	return nil
}

// prune supprime les échanges réglés depuis au moins EscrowRetentionDays jours.
func (v *escrowView) prune() (deleted []crypto.Hash) {
	for id, e := range v.st.escrows {
		if cur, ok := v.escrows[id]; ok {
			e = cur
		}
		if e.Outcome != EscrowPending && e.ClosedDay+EscrowRetentionDays <= v.day {
			deleted = append(deleted, id)
		}
	}
	for _, id := range deleted {
		delete(v.escrows, id)
	}
	return deleted
}

// EscrowRoot calcule la racine de Merkle des identifiants des opérations d'escrow d'un bloc.
func EscrowRoot(ops []EscrowOp) crypto.Hash {
	ids := make([]crypto.Hash, len(ops))
	for i := range ops {
		ids[i] = ops[i].ID()
	}
	return merkleRoot(ids)
}

// Escrow renvoie l'état d'un échange.
func (s *State) Escrow(id crypto.Hash) (Escrow, bool) {
	e, ok := s.escrows[id]
	return e, ok
}

// Reputation renvoie la réputation d'une adresse.
func (s *State) Reputation(a crypto.Address) Reputation { return s.reps[a.Hash] }

// FilterEscrowOps renvoie, dans l'ordre, les opérations applicables après les transactions txs dans
// un bloc daté blockTime (au plus max) : celles qui échouent sont écartées. Sert au proposant.
// p : paramètres de base ; ceux en vigueur ce jour-là sont recalculés depuis l'état.
func (s *State) FilterEscrowOps(txs []Transaction, ops []EscrowOp, blockTime int64, max int, p Params) []EscrowOp {
	p = s.ParamsAt(p, blockTime)
	accounts := map[addrKey]Account{}
	get := func(k addrKey) Account {
		if a, ok := accounts[k]; ok {
			return a
		}
		return s.accounts[k]
	}
	set := func(k addrKey, a Account) { accounts[k] = a }
	if _, err := applyTxs(txs, blockTime, p, get, set); err != nil {
		return nil
	}
	var out []EscrowOp
	v := s.newEscrowView(blockTime, p, get, set)
	for i := range ops {
		if len(out) == max {
			break
		}
		snapAcc, snapEsc, snapRep, snapFees := cloneMap(accounts), cloneMap(v.escrows), cloneMap(v.reps), v.fees
		if err := v.apply(&ops[i]); err != nil {
			accounts, v.escrows, v.reps, v.fees = snapAcc, snapEsc, snapRep, snapFees
			continue
		}
		out = append(out, ops[i])
	}
	return out
}

// CheckEscrowOp indique si une opération serait applicable seule dans un bloc daté blockTime.
func (s *State) CheckEscrowOp(op *EscrowOp, blockTime int64, p Params) error {
	p = s.ParamsAt(p, blockTime)
	accounts := map[addrKey]Account{}
	get := func(k addrKey) Account {
		if a, ok := accounts[k]; ok {
			return a
		}
		return s.accounts[k]
	}
	return s.newEscrowView(blockTime, p, get, func(k addrKey, a Account) { accounts[k] = a }).apply(op)
}

func (s *State) newEscrowView(blockTime int64, p Params, get func(addrKey) Account, set func(addrKey, Account)) *escrowView {
	return &escrowView{st: s, day: dayOf(blockTime), time: blockTime, p: p, get: get, set: set,
		escrows: map[crypto.Hash]Escrow{}, reps: map[addrKey]Reputation{}}
}

func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
