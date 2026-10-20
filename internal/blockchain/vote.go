package blockchain

import (
	"bytes"
	"encoding/binary"
	"errors"
	"time"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// Vote en deux temps avec verrouillage (Tendermint, adapté au rythme quotidien d'ETL).
//
//	pré-vote        à réception d'une proposition valide : « ce bloc me convient »
//	pré-engagement  quand plus de 2/3 de pré-votes pour un bloc sont vus dans le tour en cours :
//	                le nœud se VERROUILLE sur ce bloc et le signe une seconde fois
//	finalité        plus de 2/3 de pré-engagements pour le même bloc AU MÊME TOUR
//
// Un tour est identifié par (jour, tour) ; tant que la hauteur n'est pas finalisée, les tours
// continuent le lendemain. Un nœud verrouillé ne pré-vote plus que son bloc, sauf preuve de plus
// de 2/3 de pré-votes pour un autre bloc à un tour postérieur à son verrou.
//
// Sécurité : tant que moins d'un tiers des nœuds trichent, jamais deux blocs finalisés à la même
// hauteur. Continuité : une coupure ne bloque plus la chaîne — personne ne s'est verrouillé sans
// preuve, et un bloc verrouillé est re-proposé jusqu'à sa finalisation.

var (
	ErrVoteSignature     = errors.New("blockchain: signature de vote invalide")
	ErrCertificateTarget = errors.New("blockchain: certificat d'un autre bloc ou d'un autre tour")
	ErrCertificateOrder  = errors.New("blockchain: votes du certificat non triés ou en double")
	ErrCertificateVoter  = errors.New("blockchain: votant non admis")
	ErrCertificateType   = errors.New("blockchain: type de votes du certificat incorrect")
	ErrQuorum            = errors.New("blockchain: moins de 2/3 des nœuds actifs ont voté")
	ErrVoteEquivocation  = errors.New("blockchain: preuve de double vote invalide")
)

// RoundStart renvoie le début du tour round du jour blockTime : fin de la fenêtre de participation + round tours.
//
//	tour 0 : 23:00:30   tour 1 : 23:02:30   …   tour 4 : 23:08:30
func RoundStart(blockTime int64, round uint8, p Params) time.Time {
	return time.Unix(blockTime, 0).Add(p.SubmissionWindow + time.Duration(round)*p.RoundDuration)
}

// Quorum renvoie le nombre de votes nécessaires parmi n nœuds actifs : strictement plus de 2/3.
// 10 nœuds → 7 ; 9 → 7 ; 1000 → 667.
func Quorum(n int) int {
	return 2*n/3 + 1
}

// RoundID identifie un tour : le jour (heure du bloc, 23:00:00 UTC) et le tour dans la journée.
type RoundID struct {
	Day   int64
	Round uint8
}

// Before indique si r précède o dans le temps.
func (r RoundID) Before(o RoundID) bool {
	return r.Day < o.Day || (r.Day == o.Day && r.Round < o.Round)
}

// VoteType distingue les deux temps du vote.
type VoteType uint8

const (
	Prevote   VoteType = 1
	Precommit VoteType = 2
)

// Vote est la signature d'un nœud admis pour un bloc, à un tour donné.
type Vote struct {
	Voter     crypto.Address
	Type      VoteType
	Height    uint64
	Round     RoundID
	BlockHash crypto.Hash
	Signature crypto.Signature // usage block-vote
}

func voteMessage(t VoteType, height uint64, r RoundID, hash crypto.Hash) []byte {
	b := []byte{byte(t)}
	b = binary.BigEndian.AppendUint64(b, height)
	b = binary.BigEndian.AppendUint64(b, uint64(r.Day))
	b = append(b, r.Round)
	return append(b, hash[:]...)
}

// SignVote produit un vote de type t, au tour r, pour le bloc d'en-tête h.
func SignVote(key *crypto.PrivateKey, t VoteType, h *Header, r RoundID, p Params) (Vote, error) {
	hash := h.Hash()
	sig, err := key.Sign(p.ChainID, crypto.PurposeBlockVote, voteMessage(t, h.Height, r, hash))
	if err != nil {
		return Vote{}, err
	}
	return Vote{Voter: key.PublicKey().Address(p.AddressHRP), Type: t, Height: h.Height, Round: r, BlockHash: hash, Signature: sig}, nil
}

// Verify contrôle le type et la signature du vote.
func (v *Vote) Verify(p Params) error {
	if (v.Type != Prevote && v.Type != Precommit) || v.Voter.HRP != p.AddressHRP ||
		crypto.Verify(v.Voter, p.ChainID, crypto.PurposeBlockVote, voteMessage(v.Type, v.Height, v.Round, v.BlockHash), v.Signature) != nil {
		return ErrVoteSignature
	}
	return nil
}

// Certificate est un ensemble de plus de 2/3 de votes d'un même type, pour un même bloc, au même tour.
//   - Precommit : certificat de FINALITÉ, stocké avec le bloc ;
//   - Prevote : preuve de verrou, jointe à une re-proposition.
type Certificate struct {
	Type      VoteType
	Height    uint64
	Round     RoundID
	BlockHash crypto.Hash
	Votes     []Vote // triés par adresse du votant, un seul par votant
}

// NewCertificate assemble un certificat de type t, au tour r, pour l'en-tête h
// (votes invalides, d'un autre type, tour ou bloc, ou en double ignorés ; tri canonique).
func NewCertificate(t VoteType, h *Header, r RoundID, votes []Vote, p Params) Certificate {
	hash := h.Hash()
	c := Certificate{Type: t, Height: h.Height, Round: r, BlockHash: hash}
	seen := map[addrKey]bool{}
	for _, v := range votes {
		if v.Type != t || v.Height != h.Height || v.Round != r || v.BlockHash != hash || seen[v.Voter.Hash] || v.Verify(p) != nil {
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

// verifyVotes vérifie qu'un certificat de type t porte sur hash à la hauteur height,
// avec plus de 2/3 des nœuds actifs de st (état AVANT le bloc).
func verifyVotes(c *Certificate, t VoteType, height uint64, hash crypto.Hash, st *State, p Params) error {
	if c.Type != t {
		return ErrCertificateType
	}
	if c.Height != height || c.BlockHash != hash {
		return ErrCertificateTarget
	}
	for i := range c.Votes {
		v := &c.Votes[i]
		if i > 0 && bytes.Compare(c.Votes[i-1].Voter.Hash[:], v.Voter.Hash[:]) >= 0 {
			return ErrCertificateOrder
		}
		if v.Type != c.Type || v.Height != c.Height || v.Round != c.Round || v.BlockHash != c.BlockHash {
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

// VerifyCertificate vérifie qu'un certificat de finalité (pré-engagements) finalise l'en-tête h.
// st est l'état AVANT le bloc : il définit qui peut voter et le nombre de nœuds actifs.
func VerifyCertificate(c *Certificate, h *Header, st *State, p Params) error {
	return verifyVotes(c, Precommit, h.Height, h.Hash(), st, p)
}

// VerifyPOL vérifie une preuve de verrou : plus de 2/3 de pré-votes pour l'en-tête h.
func VerifyPOL(c *Certificate, h *Header, st *State, p Params) error {
	return verifyVotes(c, Prevote, h.Height, h.Hash(), st, p)
}

// VoteEquivocation prouve qu'un nœud a signé deux votes du même type, au même tour et à la même
// hauteur, pour deux blocs différents. (Voter pour des blocs différents à des tours différents est normal.)
type VoteEquivocation struct {
	A, B Vote // A.BlockHash < B.BlockHash
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

// verify contrôle la preuve ; les votes ne peuvent pas porter sur une hauteur postérieure au bloc qui l'inclut.
func (e *VoteEquivocation) verify(blockHeight uint64, p Params) error {
	if e.A.Voter != e.B.Voter || e.A.Type != e.B.Type || e.A.Height != e.B.Height || e.A.Round != e.B.Round ||
		e.A.Height > blockHeight || bytes.Compare(e.A.BlockHash[:], e.B.BlockHash[:]) >= 0 {
		return ErrVoteEquivocation
	}
	if e.A.Verify(p) != nil || e.B.Verify(p) != nil {
		return ErrVoteEquivocation
	}
	return nil
}
