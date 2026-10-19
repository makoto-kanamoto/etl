package blockchain

import (
	"bytes"
	"encoding/binary"
	"errors"
	"time"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// Votes de finalisation : un bloc est définitif quand STRICTEMENT PLUS DE 2/3 des nœuds
// admis et actifs (registre avant le bloc) l'ont signé. Tant que moins d'un tiers des nœuds
// trichent, deux blocs concurrents ne peuvent jamais être finalisés tous les deux.
//
// Sécurité avant continuité : un nœud ne vote qu'une fois par hauteur. Si aucun bloc
// n'atteint le seuil, la journée est sautée. Voter pour deux blocs différents à la même
// hauteur est une faute prouvable : exclusion automatique.

var (
	ErrVoteSignature        = errors.New("blockchain: signature de vote invalide")
	ErrCertificateTarget    = errors.New("blockchain: certificat d'un autre bloc")
	ErrCertificateOrder     = errors.New("blockchain: votes du certificat non triés ou en double")
	ErrCertificateVoter     = errors.New("blockchain: votant non admis")
	ErrQuorum               = errors.New("blockchain: moins de 2/3 des nœuds actifs ont voté")
	ErrVoteEquivocation     = errors.New("blockchain: preuve de double vote invalide")
	ErrMissingParticipation = errors.New("blockchain: le bloc omet une participation reçue avant la tolérance")
	ErrEquivocatorIncluded  = errors.New("blockchain: le bloc inclut un nœud pris en double participation")
)

// Quorum renvoie le nombre de votes nécessaires parmi n nœuds actifs : strictement plus de 2/3.
// 10 nœuds → 7 ; 9 → 7 ; 1000 → 667.
func Quorum(n int) int {
	return 2*n/3 + 1
}

// Vote est la signature d'un nœud admis pour un bloc.
type Vote struct {
	Voter     crypto.Address
	Height    uint64
	BlockHash crypto.Hash
	Signature crypto.Signature // usage block-vote
}

func voteMessage(height uint64, hash crypto.Hash) []byte {
	b := binary.BigEndian.AppendUint64(nil, height)
	return append(b, hash[:]...)
}

// SignVote produit le vote d'un nœud pour l'en-tête h.
func SignVote(key *crypto.PrivateKey, h *Header, p Params) (Vote, error) {
	hash := h.Hash()
	sig, err := key.Sign(p.ChainID, crypto.PurposeBlockVote, voteMessage(h.Height, hash))
	if err != nil {
		return Vote{}, err
	}
	return Vote{Voter: key.PublicKey().Address(p.AddressHRP), Height: h.Height, BlockHash: hash, Signature: sig}, nil
}

// Verify contrôle la signature du vote.
func (v *Vote) Verify(p Params) error {
	if v.Voter.HRP != p.AddressHRP ||
		crypto.Verify(v.Voter, p.ChainID, crypto.PurposeBlockVote, voteMessage(v.Height, v.BlockHash), v.Signature) != nil {
		return ErrVoteSignature
	}
	return nil
}

// Certificate prouve qu'un bloc a été finalisé : plus de 2/3 des nœuds actifs l'ont signé.
// Stocké avec le bloc, il permet à quiconque de vérifier la finalité sans faire confiance.
type Certificate struct {
	Height    uint64
	BlockHash crypto.Hash
	Votes     []Vote // triés par adresse du votant, un seul par votant
}

// NewCertificate assemble un certificat à partir de votes pour l'en-tête h
// (votes invalides, pour un autre bloc ou en double ignorés ; tri canonique).
func NewCertificate(h *Header, votes []Vote, p Params) Certificate {
	hash := h.Hash()
	c := Certificate{Height: h.Height, BlockHash: hash}
	seen := map[addrKey]bool{}
	for _, v := range votes {
		if v.Height != h.Height || v.BlockHash != hash || seen[v.Voter.Hash] || v.Verify(p) != nil {
			continue
		}
		seen[v.Voter.Hash] = true
		c.Votes = append(c.Votes, v)
	}
	sortVotes(c.Votes)
	return c
}

func sortVotes(vs []Vote) {
	for i := 1; i < len(vs); i++ { // tri par insertion : listes déjà presque triées
		for j := i; j > 0 && bytes.Compare(vs[j-1].Voter.Hash[:], vs[j].Voter.Hash[:]) > 0; j-- {
			vs[j-1], vs[j] = vs[j], vs[j-1]
		}
	}
}

// VerifyCertificate vérifie qu'un certificat finalise bien l'en-tête h.
// st est l'état AVANT le bloc : il définit qui peut voter et le nombre de nœuds actifs.
func VerifyCertificate(c *Certificate, h *Header, st *State, p Params) error {
	if c.Height != h.Height || c.BlockHash != h.Hash() {
		return ErrCertificateTarget
	}
	for i := range c.Votes {
		v := &c.Votes[i]
		if i > 0 && bytes.Compare(c.Votes[i-1].Voter.Hash[:], v.Voter.Hash[:]) >= 0 {
			return ErrCertificateOrder
		}
		if v.Height != c.Height || v.BlockHash != c.BlockHash {
			return ErrCertificateTarget
		}
		if !st.IsAdmitted(v.Voter) {
			return ErrCertificateVoter
		}
		if err := v.Verify(p); err != nil {
			return err
		}
	}
	if len(c.Votes) < Quorum(st.ActiveCount()) {
		return ErrQuorum
	}
	return nil
}

// VoteEquivocation prouve qu'un nœud a voté pour deux blocs différents à la même hauteur.
type VoteEquivocation struct {
	A, B Vote // même votant, même hauteur, A.BlockHash < B.BlockHash
}

// NewVoteEquivocation construit une preuve dans l'ordre canonique.
func NewVoteEquivocation(x, y Vote) VoteEquivocation {
	if bytes.Compare(x.BlockHash[:], y.BlockHash[:]) > 0 {
		x, y = y, x
	}
	return VoteEquivocation{A: x, B: y}
}

// ID identifie la preuve.
func (e *VoteEquivocation) ID() crypto.Hash {
	b := append([]byte{'V'}, appendVote(nil, &e.A)...)
	return crypto.Sum(crypto.ContextRegistry, appendVote(b, &e.B))
}

// verify contrôle la preuve ; les votes doivent porter sur une hauteur antérieure au bloc qui l'inclut.
func (e *VoteEquivocation) verify(blockHeight uint64, p Params) error {
	if e.A.Voter != e.B.Voter || e.A.Height != e.B.Height || e.A.Height >= blockHeight ||
		bytes.Compare(e.A.BlockHash[:], e.B.BlockHash[:]) >= 0 {
		return ErrVoteEquivocation
	}
	if e.A.Verify(p) != nil || e.B.Verify(p) != nil {
		return ErrVoteEquivocation
	}
	return nil
}

// CheckProposal est ce qu'un nœud honnête vérifie avant de voter pour un bloc :
//   - le bloc est valide et s'applique à l'état (StateRoot compris) ;
//   - il contient toutes les participations valides que ce nœud a reçues AVANT
//     blockTime + InclusionCutoff (23:00:25) : une participation arrivée plus tard a pu
//     ne pas atteindre le proposant, son absence ne justifie pas un refus ;
//   - il n'inclut pas un nœud que ce nœud a vu participer deux fois AVANT la même tolérance.
//     Sinon, un seul nœud malveillant pourrait envoyer une seconde participation à 23:00:29
//     à un tiers des nœuds seulement, les faire refuser, et faire sauter la journée.
//
// recv : toutes les participations reçues par ce nœud pour ce jour.
func CheckProposal(b *Block, prev *Header, st *State, recv []Received, p Params) error {
	if err := ValidateBlock(b, prev, p, st.IsAdmitted); err != nil {
		return err
	}
	if _, err := st.Prepare(b, p); err != nil {
		return err
	}
	ts := b.Header.Timestamp
	cutoff := time.Unix(ts, 0).Add(p.InclusionCutoff)
	var early []Received
	for _, r := range recv {
		if r.ReceivedAt.Before(cutoff) {
			early = append(early, r)
		}
	}
	expected := Eligible(early, prev, ts, p, func(a crypto.Address) Role { return st.Role(a, ts, p) })

	included := map[addrKey]bool{}
	for i := range b.Participants {
		included[b.Participants[i].Miner.Hash] = true
	}
	for i := range b.Observations {
		included[b.Observations[i].Miner.Hash] = true
	}
	for _, pts := range [][]Participation{expected.Participants, expected.Observations} {
		for i := range pts {
			if !included[pts[i].Miner.Hash] {
				return ErrMissingParticipation
			}
		}
	}
	// Vu en double participation par ce nœud avant la tolérance : doit être absent du bloc.
	cheaters := expected.Equivocations
	for i := range cheaters {
		if included[cheaters[i].A.Miner.Hash] {
			return ErrEquivocatorIncluded
		}
	}
	return nil
}
