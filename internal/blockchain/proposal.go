package blockchain

import (
	"encoding/binary"
	"errors"
	"time"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// Proposal est la proposition d'un bloc à un tour donné.
//
// Un seul proposant légitime par tour : au tour (jour, r), le r-ième ticket parmi les participations
// de CE jour (Nomination). Il propose :
//   - un NOUVEAU bloc, daté du jour et dont les participants sont la nomination ; ou
//   - une RE-PROPOSITION d'un bloc antérieur, accompagnée de la preuve (POL) que plus de 2/3 des
//     nœuds l'ont pré-voté à un tour précédent. Un bloc finalisé en retard garde sa date et ses
//     participants ; les participations du jour du rattrapage ne sont pas récompensées.
type Proposal struct {
	Round      RoundID
	Block      Block
	Nomination []Participation  // participations du jour du tour, par ticket croissant
	POL        *Certificate     // re-proposition uniquement : pré-votes > 2/3 pour Block
	Signature  crypto.Signature // du proposant (usage proposal)
}

var (
	ErrProposalRound        = errors.New("blockchain: tour de proposition invalide")
	ErrProposalNomination   = errors.New("blockchain: participations du tour invalides")
	ErrProposalFresh        = errors.New("blockchain: nouveau bloc incohérent avec son tour")
	ErrProposalPOL          = errors.New("blockchain: preuve de verrou invalide")
	ErrProposalSignature    = errors.New("blockchain: proposition mal signée")
	ErrMissingParticipation = errors.New("blockchain: la proposition omet une participation reçue avant la tolérance")
	ErrEquivocatorIncluded  = errors.New("blockchain: la proposition inclut un nœud pris en double participation")
	ErrRoundTooEarly        = errors.New("blockchain: proposition reçue avant le début de son tour")
)

// Proposer renvoie le proposant désigné par la nomination (adresse vide si le tour est hors liste).
func (pr *Proposal) Proposer() crypto.Address {
	if int(pr.Round.Round) >= len(pr.Nomination) {
		return crypto.Address{}
	}
	return pr.Nomination[pr.Round.Round].Miner
}

func (pr *Proposal) message() []byte {
	b := binary.BigEndian.AppendUint64(nil, pr.Block.Header.Height)
	b = binary.BigEndian.AppendUint64(b, uint64(pr.Round.Day))
	b = append(b, pr.Round.Round)
	h := pr.Block.Header.Hash()
	b = append(b, h[:]...)
	nom := ParticipantsRoot(pr.Nomination)
	b = append(b, nom[:]...)
	if pr.POL == nil {
		return append(b, 0)
	}
	b = append(b, 1)
	b = binary.BigEndian.AppendUint64(b, uint64(pr.POL.Round.Day))
	return append(b, pr.POL.Round.Round)
}

// Sign signe la proposition avec la clé du proposant.
func (pr *Proposal) Sign(key *crypto.PrivateKey, p Params) (err error) {
	pr.Signature, err = key.Sign(p.ChainID, crypto.PurposeProposal, pr.message())
	return err
}

// VerifySignature contrôle la signature du proposant désigné (sans contexte).
func (pr *Proposal) VerifySignature(p Params) error {
	proposer := pr.Proposer()
	if proposer.HRP != p.AddressHRP || crypto.Verify(proposer, p.ChainID, crypto.PurposeProposal, pr.message(), pr.Signature) != nil {
		return ErrProposalSignature
	}
	return nil
}

// ValidateProposal vérifie tout ce qui ne dépend pas de la vue locale du nœud :
// tour, nomination, signature, validité du bloc (StateRoot compris), cohérence d'un nouveau bloc
// ou preuve de verrou d'une re-proposition. Un échec désigne un proposant fautif.
func ValidateProposal(pr *Proposal, prev *Header, st *State, p Params) error {
	r := pr.Round
	if int(r.Round) >= p.MaxRounds || !isBlockTime(r.Day, p) || r.Day <= prev.Timestamp || int(r.Round) >= len(pr.Nomination) {
		return ErrProposalRound
	}
	prevHash := prev.Hash()
	var prevTicket crypto.Hash
	for i := range pr.Nomination {
		pt := &pr.Nomination[i]
		if pt.Verify(prevHash, r.Day, p) != nil || !st.IsAdmitted(pt.Miner) {
			return ErrProposalNomination
		}
		t := Ticket(prevHash, pt.Miner)
		if i > 0 && t.Cmp(prevTicket) <= 0 {
			return ErrProposalNomination
		}
		prevTicket = t
	}
	if err := pr.VerifySignature(p); err != nil {
		return err
	}
	b := &pr.Block
	if err := ValidateBlock(b, prev, p, st.IsAdmitted); err != nil {
		return err
	}
	if _, err := st.Prepare(b, p); err != nil {
		return err
	}
	if pr.POL == nil { // nouveau bloc : daté du tour, participants = nomination
		if b.Header.Timestamp != r.Day || b.Header.Round != r.Round || b.Header.ParticipantsRoot != ParticipantsRoot(pr.Nomination) {
			return ErrProposalFresh
		}
		return nil
	}
	if !pr.POL.Round.Before(r) || VerifyPOL(pr.POL, &b.Header, st, p) != nil {
		return ErrProposalPOL
	}
	return nil
}

// CheckProposal est ce qu'un nœud honnête vérifie avant de pré-voter :
//   - la proposition n'arrive pas avant le début de son tour (à ClockTolerance près) ;
//   - elle est objectivement valide (ValidateProposal) ;
//   - sa nomination contient toutes les participations valides reçues par CE nœud avant
//     23:00:25 (une participation plus tardive a pu ne pas atteindre le proposant), et, pour un
//     nouveau bloc, toutes les observations de candidats ;
//   - elle n'inclut pas un nœud vu participer deux fois avant la même tolérance.
//
// Un nœud exclu par le bloc reste dans sa nomination (le rang du proposant en dépend) : il ne touche
// simplement pas sa part (voir State.Prepare). Les trois dernières erreurs (ErrRoundTooEarly, ErrMissingParticipation, ErrEquivocatorIncluded)
// dépendent de la vue locale : le proposant peut être honnête.
func CheckProposal(pr *Proposal, prev *Header, st *State, recv []Received, now time.Time, p Params) error {
	r := pr.Round
	if now.Before(RoundStart(r.Day, r.Round, p).Add(-p.ClockTolerance)) {
		return ErrRoundTooEarly
	}
	if err := ValidateProposal(pr, prev, st, p); err != nil {
		return err
	}
	cutoff := time.Unix(r.Day, 0).Add(p.InclusionCutoff)
	var early []Received
	for _, rc := range recv {
		if rc.ReceivedAt.Before(cutoff) {
			early = append(early, rc)
		}
	}
	expected := Eligible(early, prev, r.Day, p, func(a crypto.Address) Role { return st.Role(a, r.Day, p) })

	included := map[addrKey]bool{}
	for i := range pr.Nomination {
		included[pr.Nomination[i].Miner.Hash] = true
	}
	fresh := pr.POL == nil
	if fresh {
		for i := range pr.Block.Observations {
			included[pr.Block.Observations[i].Miner.Hash] = true
		}
	}
	required := expected.Participants
	if fresh {
		required = append(append([]Participation(nil), required...), expected.Observations...)
	}
	for i := range required {
		k := required[i].Miner.Hash
		if !included[k] {
			return ErrMissingParticipation
		}
	}
	for i := range expected.Equivocations {
		if included[expected.Equivocations[i].A.Miner.Hash] {
			return ErrEquivocatorIncluded
		}
	}
	return nil
}
