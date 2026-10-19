package blockchain

import (
	"encoding/binary"
	"errors"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// Encodage binaire canonique des objets du protocole.
// Utilisé pour le stockage et le réseau : un seul format, une seule façon d'encoder chaque objet.
// Les décodeurs reçoivent des données non fiables : ils refusent toute entrée tronquée,
// trop longue, aux booléens non canoniques ou aux listes de taille incohérente,
// de sorte que décoder puis ré-encoder redonne exactement les mêmes octets.
//
// Les adresses sont encodées sans leur préfixe (HRP) : il est implicite pour un réseau
// et fourni au décodage. L'identifiant d'une transaction n'est pas transmis : il est recalculé.

var ErrDecode = errors.New("blockchain: encodage invalide")

const (
	participationSize = crypto.AddressHashSize + 8 + crypto.SignatureSize
	registrationSize  = 2*crypto.AddressHashSize + 8 + 2*crypto.SignatureSize
	voteSize          = crypto.AddressHashSize + 8 + crypto.HashSize + crypto.SignatureSize
	certVoteSize      = crypto.AddressHashSize + crypto.SignatureSize
	txSize            = txBodySize + crypto.SignatureSize
	accountSize       = 16
	nodeSize          = 1 + crypto.AddressHashSize + 1 + 8 + 4 + 1 + 8 + 8 + 1 + 8 + 8
)

// --- écriture ---

func appendAddr(b []byte, a crypto.Address) []byte { return append(b, a.Hash[:]...) }

func appendBool(b []byte, v bool) []byte {
	if v {
		return append(b, 1)
	}
	return append(b, 0)
}

func appendParticipation(b []byte, pt *Participation) []byte {
	b = appendAddr(b, pt.Miner)
	b = binary.BigEndian.AppendUint64(b, pt.Nonce)
	return append(b, pt.Signature[:]...)
}

func appendTx(b []byte, tx *Transaction) []byte {
	b = append(b, tx.Body()...)
	return append(b, tx.Signature[:]...)
}

func appendRegistration(b []byte, r *Registration) []byte {
	b = append(b, r.message()...)
	b = append(b, r.CandidateSig[:]...)
	return append(b, r.SponsorSig[:]...)
}

func appendVote(b []byte, v *Vote) []byte {
	b = appendAddr(b, v.Voter)
	b = binary.BigEndian.AppendUint64(b, v.Height)
	b = append(b, v.BlockHash[:]...)
	return append(b, v.Signature[:]...)
}

// EncodeVote renvoie l'encodage d'un vote (pour le réseau).
func EncodeVote(v *Vote) []byte { return appendVote(nil, v) }

// EncodeCertificate renvoie l'encodage compact d'un certificat :
// hauteur et hash une seule fois, puis (votant, signature) pour chaque vote.
func EncodeCertificate(c *Certificate) []byte {
	b := binary.BigEndian.AppendUint64(nil, c.Height)
	b = append(b, c.BlockHash[:]...)
	b = binary.BigEndian.AppendUint32(b, uint32(len(c.Votes)))
	for i := range c.Votes {
		b = appendAddr(b, c.Votes[i].Voter)
		b = append(b, c.Votes[i].Signature[:]...)
	}
	return b
}

// AppendAccount ajoute l'encodage d'un compte.
func AppendAccount(b []byte, a Account) []byte {
	b = binary.BigEndian.AppendUint64(b, a.Balance)
	return binary.BigEndian.AppendUint64(b, a.Nonce)
}

// AppendNode ajoute l'encodage d'une entrée du registre.
func AppendNode(b []byte, n Node) []byte {
	b = append(b, byte(n.Status))
	b = append(b, n.Sponsor[:]...)
	b = appendBool(b, n.Founder)
	b = binary.BigEndian.AppendUint64(b, uint64(n.CandidateDay))
	b = binary.BigEndian.AppendUint32(b, uint32(n.ObservedDays))
	b = appendBool(b, n.Reactivating)
	b = binary.BigEndian.AppendUint64(b, uint64(n.AdmittedDay))
	b = binary.BigEndian.AppendUint64(b, uint64(n.LastParticipationDay))
	b = appendBool(b, n.HasSponsored)
	b = binary.BigEndian.AppendUint64(b, uint64(n.LastSponsorDay))
	return binary.BigEndian.AppendUint64(b, uint64(n.SponsorBanUntil))
}

// EncodeTransaction renvoie l'encodage d'une transaction (corps + signature).
func EncodeTransaction(tx *Transaction) []byte { return appendTx(nil, tx) }

// EncodeBlock renvoie l'encodage complet d'un bloc.
func EncodeBlock(blk *Block) []byte {
	b := blk.Header.Encode()
	b = append(b, blk.Signature[:]...)
	b = binary.BigEndian.AppendUint32(b, uint32(len(blk.Participants)))
	for i := range blk.Participants {
		b = appendParticipation(b, &blk.Participants[i])
	}
	b = binary.BigEndian.AppendUint32(b, uint32(len(blk.Txs)))
	for i := range blk.Txs {
		b = appendTx(b, &blk.Txs[i])
	}
	b = binary.BigEndian.AppendUint32(b, uint32(len(blk.Registrations)))
	for i := range blk.Registrations {
		b = appendRegistration(b, &blk.Registrations[i])
	}
	b = binary.BigEndian.AppendUint32(b, uint32(len(blk.Observations)))
	for i := range blk.Observations {
		b = appendParticipation(b, &blk.Observations[i])
	}
	b = binary.BigEndian.AppendUint32(b, uint32(len(blk.Equivocations)))
	for i := range blk.Equivocations {
		b = appendParticipation(b, &blk.Equivocations[i].A)
		b = appendParticipation(b, &blk.Equivocations[i].B)
	}
	b = binary.BigEndian.AppendUint32(b, uint32(len(blk.VoteEquivocations)))
	for i := range blk.VoteEquivocations {
		b = appendVote(b, &blk.VoteEquivocations[i].A)
		b = appendVote(b, &blk.VoteEquivocations[i].B)
	}
	b = binary.BigEndian.AppendUint32(b, uint32(len(blk.Founders)))
	for _, f := range blk.Founders {
		b = appendAddr(b, f)
	}
	return b
}

// --- lecture ---

type reader struct {
	b   []byte
	hrp string
	err error
}

func (r *reader) take(n int) []byte {
	if r.err != nil || n < 0 || len(r.b) < n {
		r.err = ErrDecode
		return make([]byte, max(n, 0)) // tailles fixes ou ≤ 255 : jamais d'allocation démesurée
	}
	out := r.b[:n]
	r.b = r.b[n:]
	return out
}

func (r *reader) u8() byte    { return r.take(1)[0] }
func (r *reader) u32() uint32 { return binary.BigEndian.Uint32(r.take(4)) }
func (r *reader) u64() uint64 { return binary.BigEndian.Uint64(r.take(8)) }
func (r *reader) i64() int64  { return int64(r.u64()) }

func (r *reader) boolean() bool {
	switch r.u8() {
	case 0:
		return false
	case 1:
		return true
	}
	r.err = ErrDecode
	return false
}

func (r *reader) hash() (h crypto.Hash) {
	copy(h[:], r.take(crypto.HashSize))
	return h
}

func (r *reader) addr() crypto.Address {
	a := crypto.Address{HRP: r.hrp}
	copy(a.Hash[:], r.take(crypto.AddressHashSize))
	return a
}

func (r *reader) sig() (s crypto.Signature) {
	copy(s[:], r.take(crypto.SignatureSize))
	return s
}

// count lit une taille de liste et refuse toute taille impossible au vu des octets restants
// (aucune allocation démesurée sur une entrée hostile).
func (r *reader) count(itemSize int) int {
	n := r.u32()
	if r.err == nil && uint64(n)*uint64(itemSize) > uint64(len(r.b)) {
		r.err = ErrDecode
	}
	if r.err != nil {
		return 0
	}
	return int(n)
}

func (r *reader) done() error {
	if r.err == nil && len(r.b) != 0 {
		r.err = ErrDecode
	}
	return r.err
}

func (r *reader) participation() Participation {
	return Participation{Miner: r.addr(), Nonce: r.u64(), Signature: r.sig()}
}

func (r *reader) tx() Transaction {
	if r.u8() != TxVersion {
		r.err = ErrDecode
	}
	tx := Transaction{From: r.addr(), To: r.addr(), Amount: r.u64(), Fee: r.u64(), Nonce: r.u64(), Timestamp: r.i64(), Signature: r.sig()}
	tx.ID = tx.ComputeID()
	return tx
}

func (r *reader) vote() Vote {
	return Vote{Voter: r.addr(), Height: r.u64(), BlockHash: r.hash(), Signature: r.sig()}
}

func (r *reader) registration() Registration {
	reg := Registration{Candidate: r.addr(), Sponsor: r.addr(), Day: r.i64()}
	reg.CandidateSig, reg.SponsorSig = r.sig(), r.sig()
	return reg
}

func (r *reader) header() Header {
	if r.u8() != BlockVersion {
		r.err = ErrDecode
	}
	h := Header{Height: r.u64(), PrevHash: r.hash(), Timestamp: r.i64(), TxRoot: r.hash(),
		ParticipantsRoot: r.hash(), RegistryRoot: r.hash(), StateRoot: r.hash()}
	if r.boolean() {
		ipc := IPCRecord{Value: r.u64()}
		ipc.Period = string(r.take(int(r.u8())))
		copy(ipc.SourceSHA256[:], r.take(32))
		h.IPC = &ipc
	}
	h.Proposer = r.addr()
	return h
}

// DecodeHeader décode un en-tête ; hrp est le préfixe d'adresse du réseau.
func DecodeHeader(b []byte, hrp string) (Header, error) {
	r := &reader{b: b, hrp: hrp}
	h := r.header()
	return h, r.done()
}

// DecodeTransaction décode une transaction et recalcule son identifiant.
func DecodeTransaction(b []byte, hrp string) (Transaction, error) {
	r := &reader{b: b, hrp: hrp}
	tx := r.tx()
	return tx, r.done()
}

// DecodeVote décode un vote.
func DecodeVote(b []byte, hrp string) (Vote, error) {
	r := &reader{b: b, hrp: hrp}
	v := r.vote()
	return v, r.done()
}

// DecodeCertificate décode un certificat.
func DecodeCertificate(b []byte, hrp string) (Certificate, error) {
	r := &reader{b: b, hrp: hrp}
	c := Certificate{Height: r.u64(), BlockHash: r.hash()}
	if n := r.count(certVoteSize); n > 0 {
		c.Votes = make([]Vote, n)
		for i := range c.Votes {
			c.Votes[i] = Vote{Voter: r.addr(), Height: c.Height, BlockHash: c.BlockHash, Signature: r.sig()}
		}
	}
	return c, r.done()
}

// DecodeAccount décode un compte.
func DecodeAccount(b []byte) (Account, error) {
	r := &reader{b: b}
	a := Account{Balance: r.u64(), Nonce: r.u64()}
	return a, r.done()
}

// DecodeNode décode une entrée du registre.
func DecodeNode(b []byte) (Node, error) {
	r := &reader{b: b}
	n := Node{Status: NodeStatus(r.u8())}
	if n.Status > StatusExcluded {
		r.err = ErrDecode
	}
	copy(n.Sponsor[:], r.take(crypto.AddressHashSize))
	n.Founder = r.boolean()
	n.CandidateDay = r.i64()
	n.ObservedDays = int(r.u32())
	n.Reactivating = r.boolean()
	n.AdmittedDay = r.i64()
	n.LastParticipationDay = r.i64()
	n.HasSponsored = r.boolean()
	n.LastSponsorDay = r.i64()
	n.SponsorBanUntil = r.i64()
	return n, r.done()
}

// DecodeBlock décode un bloc complet ; hrp est le préfixe d'adresse du réseau.
// Le décodage ne valide pas le bloc : voir ValidateBlock et State.ApplyBlock.
func DecodeBlock(b []byte, hrp string) (Block, error) {
	r := &reader{b: b, hrp: hrp}
	blk := Block{Header: r.header(), Signature: r.sig()}
	if n := r.count(participationSize); n > 0 {
		blk.Participants = make([]Participation, n)
		for i := range blk.Participants {
			blk.Participants[i] = r.participation()
		}
	}
	if n := r.count(txSize); n > 0 {
		blk.Txs = make([]Transaction, n)
		for i := range blk.Txs {
			blk.Txs[i] = r.tx()
		}
	}
	if n := r.count(registrationSize); n > 0 {
		blk.Registrations = make([]Registration, n)
		for i := range blk.Registrations {
			blk.Registrations[i] = r.registration()
		}
	}
	if n := r.count(participationSize); n > 0 {
		blk.Observations = make([]Participation, n)
		for i := range blk.Observations {
			blk.Observations[i] = r.participation()
		}
	}
	if n := r.count(2 * participationSize); n > 0 {
		blk.Equivocations = make([]Equivocation, n)
		for i := range blk.Equivocations {
			blk.Equivocations[i] = Equivocation{A: r.participation(), B: r.participation()}
		}
	}
	if n := r.count(2 * voteSize); n > 0 {
		blk.VoteEquivocations = make([]VoteEquivocation, n)
		for i := range blk.VoteEquivocations {
			blk.VoteEquivocations[i] = VoteEquivocation{A: r.vote(), B: r.vote()}
		}
	}
	if n := r.count(crypto.AddressHashSize); n > 0 {
		blk.Founders = make([]crypto.Address, n)
		for i := range blk.Founders {
			blk.Founders[i] = r.addr()
		}
	}
	return blk, r.done()
}
