package blockchain

import (
	"bytes"
	"encoding/binary"
	"errors"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// Registre des nœuds admis : la règle « 1 membre = 1 nœud » et la sécurité économique
// du partage de récompense reposent sur lui. Il fait partie de l'état on-chain.
//
// Cycle de vie : candidat (parrainé) → observation 7 jours → admis → inactif (30 jours d'absence)
// → candidat à nouveau (réactivation, sans parrain). Exclu : définitif.
// Toutes les durées sont en jours UTC : jour = horodatage du bloc ÷ 86 400.

// NodeStatus est le statut d'un nœud dans le registre.
type NodeStatus uint8

const (
	StatusUnknown   NodeStatus = iota // absent du registre
	StatusCandidate                   // en observation
	StatusAdmitted                    // admis et actif : part de récompense, ticket, vote
	StatusInactive                    // admis mais absent depuis InactivityDays
	StatusExcluded                    // exclu définitivement
)

// String renvoie le nom du statut (API, journaux).
func (s NodeStatus) String() string {
	switch s {
	case StatusCandidate:
		return "candidate"
	case StatusAdmitted:
		return "admitted"
	case StatusInactive:
		return "inactive"
	case StatusExcluded:
		return "excluded"
	}
	return "unknown"
}

// Node est l'entrée d'un nœud dans le registre.
type Node struct {
	Status       NodeStatus
	Sponsor      addrKey // zéro pour un fondateur ou une réactivation
	Founder      bool    // fondateur du genesis : dispensé de l'ancienneté pour parrainer
	CandidateDay int64   // jour de la candidature
	ObservedDays int     // participations pendant l'observation
	Reactivating bool    // candidat qui était inactif : en cas d'échec, redevient inactif

	AdmittedDay          int64 // jour d'admission : point de départ de l'ancienneté
	LastParticipationDay int64

	HasSponsored    bool
	LastSponsorDay  int64
	SponsorBanUntil int64 // jour à partir duquel le nœud peut de nouveau parrainer
}

var (
	ErrFounders            = errors.New("blockchain: liste des fondateurs invalide")
	ErrRegistrationSig     = errors.New("blockchain: signature de candidature invalide")
	ErrRegistrationDay     = errors.New("blockchain: candidature expirée ou prématurée")
	ErrRegistrationStatus  = errors.New("blockchain: le candidat ne peut pas candidater")
	ErrSponsorNotEligible  = errors.New("blockchain: parrain non éligible")
	ErrObservationStatus   = errors.New("blockchain: observation d'un nœud qui n'est pas candidat")
	ErrEquivocationInvalid = errors.New("blockchain: preuve de double participation invalide")
	ErrEquivocatorPresent  = errors.New("blockchain: un nœud exclu figure parmi les participants")
	ErrNotAdmitted         = errors.New("blockchain: participant non admis")
	ErrRegistryOrder       = errors.New("blockchain: éléments du registre non triés ou en double")
)

// Registration est une candidature (avec parrain) ou une réactivation (sans parrain).
type Registration struct {
	Candidate    crypto.Address
	Sponsor      crypto.Address // Hash nul pour une réactivation
	Day          int64          // jour de signature : valable RegistrationDays jours (anti-rejeu)
	CandidateSig crypto.Signature
	SponsorSig   crypto.Signature
}

// IsReactivation indique une réactivation d'un nœud inactif (pas de parrain).
func (r *Registration) IsReactivation() bool {
	return r.Sponsor.Hash == addrKey{}
}

func (r *Registration) message() []byte {
	b := make([]byte, 0, 2*crypto.AddressHashSize+8)
	b = append(b, r.Candidate.Hash[:]...)
	b = append(b, r.Sponsor.Hash[:]...)
	return binary.BigEndian.AppendUint64(b, uint64(r.Day))
}

// ID identifie la candidature (signatures comprises).
func (r *Registration) ID() crypto.Hash {
	b := append([]byte{'R'}, r.message()...)
	b = append(b, r.CandidateSig[:]...)
	b = append(b, r.SponsorSig[:]...)
	return crypto.Sum(crypto.ContextRegistry, b)
}

// SignCandidate signe la candidature avec la clé du candidat.
func (r *Registration) SignCandidate(key *crypto.PrivateKey, chainID string) (err error) {
	r.CandidateSig, err = key.Sign(chainID, crypto.PurposeRegistration, r.message())
	return err
}

// SignSponsor contresigne la candidature avec la clé du parrain.
func (r *Registration) SignSponsor(key *crypto.PrivateKey, chainID string) (err error) {
	r.SponsorSig, err = key.Sign(chainID, crypto.PurposeSponsorship, r.message())
	return err
}

// VerifySignatures contrôle la signature du candidat et, hors réactivation, celle du parrain.
func (r *Registration) VerifySignatures(p Params) error {
	if r.Candidate.HRP != p.AddressHRP || crypto.Verify(r.Candidate, p.ChainID, crypto.PurposeRegistration, r.message(), r.CandidateSig) != nil {
		return ErrRegistrationSig
	}
	if r.IsReactivation() {
		return nil
	}
	if r.Sponsor.HRP != p.AddressHRP || crypto.Verify(r.Sponsor, p.ChainID, crypto.PurposeSponsorship, r.message(), r.SponsorSig) != nil {
		return ErrRegistrationSig
	}
	return nil
}

// Equivocation prouve qu'un nœud a soumis deux participations différentes le même jour.
// Les deux participations sont signées par lui : la preuve est irréfutable.
type Equivocation struct {
	A, B Participation // A.ID() < B.ID()
}

// ID identifie la preuve.
func (e *Equivocation) ID() crypto.Hash {
	a, b := e.A.ID(), e.B.ID()
	buf := append([]byte{'E'}, a[:]...)
	return crypto.Sum(crypto.ContextRegistry, append(buf, b[:]...))
}

// NewEquivocation construit une preuve dans l'ordre canonique.
func NewEquivocation(x, y Participation) Equivocation {
	if x.ID().Cmp(y.ID()) > 0 {
		x, y = y, x
	}
	return Equivocation{A: x, B: y}
}

func (e *Equivocation) verify(prevHash crypto.Hash, blockTime int64, p Params) error {
	if e.A.Miner != e.B.Miner || e.A.ID().Cmp(e.B.ID()) >= 0 {
		return ErrEquivocationInvalid
	}
	if e.A.Verify(prevHash, blockTime, p) != nil || e.B.Verify(prevHash, blockTime, p) != nil {
		return ErrEquivocationInvalid
	}
	return nil
}

// RegistryRoot engage l'en-tête sur les enregistrements, observations et preuves du bloc.
func RegistryRoot(regs []Registration, obs []Participation, eqs []Equivocation, veqs []VoteEquivocation) crypto.Hash {
	regIDs := make([]crypto.Hash, len(regs))
	for i := range regs {
		regIDs[i] = regs[i].ID()
	}
	eqIDs := make([]crypto.Hash, len(eqs))
	for i := range eqs {
		eqIDs[i] = eqs[i].ID()
	}
	veqIDs := make([]crypto.Hash, len(veqs))
	for i := range veqs {
		veqIDs[i] = veqs[i].ID()
	}
	return merkleRoot([]crypto.Hash{merkleRoot(regIDs), ParticipantsRoot(obs), merkleRoot(eqIDs), merkleRoot(veqIDs)})
}

// dayOf renvoie le jour UTC d'un horodatage.
func dayOf(ts int64) int64 { return ts / secondsPerDay }

// registry est une copie de travail du registre pendant l'application d'un bloc.
type registry map[addrKey]Node

func (r registry) clone() registry {
	c := make(registry, len(r))
	for k, v := range r {
		c[k] = v
	}
	return c
}

func (r registry) activeCount() int {
	n := 0
	for _, node := range r {
		if node.Status == StatusAdmitted {
			n++
		}
	}
	return n
}

// applyFounders inscrit les fondateurs du genesis, admis dès le jour 0.
func (r registry) applyFounders(founders []crypto.Address, day int64, p Params) error {
	if len(founders) < p.MinFounders {
		return ErrFounders
	}
	for _, f := range founders {
		if f.HRP != p.AddressHRP || r[f.Hash].Status != StatusUnknown || f.Hash == (addrKey{}) {
			return ErrFounders
		}
		r[f.Hash] = Node{Status: StatusAdmitted, Founder: true, AdmittedDay: day, LastParticipationDay: day}
	}
	return nil
}

// applyEquivocations exclut les nœuds pris en double participation.
func (r registry) applyEquivocations(eqs []Equivocation, prevHash crypto.Hash, blockTime int64, p Params) error {
	for i := range eqs {
		e := &eqs[i]
		if i > 0 && bytes.Compare(eqs[i-1].A.Miner.Hash[:], e.A.Miner.Hash[:]) >= 0 {
			return ErrRegistryOrder
		}
		if err := e.verify(prevHash, blockTime, p); err != nil {
			return err
		}
		node := r[e.A.Miner.Hash]
		if node.Status == StatusUnknown || node.Status == StatusExcluded {
			return ErrEquivocationInvalid
		}
		node.Status = StatusExcluded
		r[e.A.Miner.Hash] = node
	}
	return nil
}

// applyVoteEquivocations exclut les nœuds pris en double vote.
func (r registry) applyVoteEquivocations(veqs []VoteEquivocation, blockHeight uint64, p Params) error {
	for i := range veqs {
		e := &veqs[i]
		if i > 0 && bytes.Compare(veqs[i-1].A.Voter.Hash[:], e.A.Voter.Hash[:]) >= 0 {
			return ErrRegistryOrder
		}
		if err := e.verify(blockHeight, p); err != nil {
			return err
		}
		node := r[e.A.Voter.Hash]
		if node.Status == StatusUnknown || node.Status == StatusExcluded {
			return ErrVoteEquivocation
		}
		node.Status = StatusExcluded
		r[e.A.Voter.Hash] = node
	}
	return nil
}

// applyRegistrations traite candidatures et réactivations.
func (r registry) applyRegistrations(regs []Registration, day int64, p Params) error {
	for i := range regs {
		reg := &regs[i]
		if i > 0 && bytes.Compare(regs[i-1].Candidate.Hash[:], reg.Candidate.Hash[:]) >= 0 {
			return ErrRegistryOrder
		}
		if day < reg.Day || day >= reg.Day+p.RegistrationDays {
			return ErrRegistrationDay
		}
		if err := reg.VerifySignatures(p); err != nil {
			return err
		}
		cand := r[reg.Candidate.Hash]

		if reg.IsReactivation() {
			if cand.Status != StatusInactive {
				return ErrRegistrationStatus
			}
			// Une réactivation remet l'ancienneté à zéro, y compris pour un fondateur.
			cand.Status, cand.CandidateDay, cand.ObservedDays, cand.Reactivating, cand.Founder = StatusCandidate, day, 0, true, false
			r[reg.Candidate.Hash] = cand
			continue
		}

		if cand.Status != StatusUnknown || reg.Candidate == reg.Sponsor {
			return ErrRegistrationStatus
		}
		sponsor := r[reg.Sponsor.Hash]
		if sponsor.Status != StatusAdmitted ||
			(!sponsor.Founder && day-sponsor.AdmittedDay < p.SponsorSeniorityDays) ||
			(sponsor.HasSponsored && day-sponsor.LastSponsorDay < p.SponsorCooldownDays) ||
			day < sponsor.SponsorBanUntil {
			return ErrSponsorNotEligible
		}
		sponsor.HasSponsored, sponsor.LastSponsorDay = true, day
		r[reg.Sponsor.Hash] = sponsor
		r[reg.Candidate.Hash] = Node{Status: StatusCandidate, Sponsor: reg.Sponsor.Hash, CandidateDay: day}
	}
	return nil
}

// applyObservations compte les participations des candidats.
func (r registry) applyObservations(obs []Participation, prevHash crypto.Hash, blockTime int64, p Params) error {
	day := dayOf(blockTime)
	for i := range obs {
		pt := &obs[i]
		if i > 0 && bytes.Compare(obs[i-1].Miner.Hash[:], pt.Miner.Hash[:]) >= 0 {
			return ErrRegistryOrder
		}
		if err := pt.Verify(prevHash, blockTime, p); err != nil {
			return err
		}
		node := r[pt.Miner.Hash]
		// Le jour de la candidature ne compte pas : l'observation couvre les 7 jours suivants.
		if node.Status != StatusCandidate || day <= node.CandidateDay || day > node.CandidateDay+p.ObservationDays {
			return ErrObservationStatus
		}
		node.ObservedDays++
		r[pt.Miner.Hash] = node
	}
	return nil
}

// excludedBy renvoie les nœuds que le bloc exclut : exclusions votées, double participation, double vote.
func excludedBy(b *Block) map[addrKey]bool {
	out := map[addrKey]bool{}
	for i := range b.Exclusions {
		out[b.Exclusions[i].Target.Hash] = true
	}
	for i := range b.Equivocations {
		out[b.Equivocations[i].A.Miner.Hash] = true
	}
	for i := range b.VoteEquivocations {
		out[b.VoteEquivocations[i].A.Voter.Hash] = true
	}
	return out
}

// applyParticipants vérifie que les participants étaient admis avant le bloc et met à jour leur
// présence (sauf pour ceux que ce bloc exclut). Un nœud déjà exclu ne peut pas participer.
func (r registry) applyParticipants(pts []Participation, excludedNow map[addrKey]bool, day int64) error {
	for i := range pts {
		k := pts[i].Miner.Hash
		node := r[k]
		if node.Status == StatusExcluded {
			return ErrEquivocatorPresent
		}
		if node.Status != StatusAdmitted {
			return ErrNotAdmitted
		}
		if !excludedNow[k] {
			node.LastParticipationDay = day
			r[k] = node
		}
	}
	return nil
}

// applyTransitions fait avancer chaque nœud dans son cycle de vie en fin de bloc.
// Chaque transition ne dépend que du nœud lui-même : l'ordre de parcours est sans effet.
func (r registry) applyTransitions(day int64, p Params) {
	for k, node := range r {
		switch node.Status {
		case StatusCandidate:
			if day < node.CandidateDay+p.ObservationDays {
				continue
			}
			switch {
			case node.ObservedDays >= p.ObservationMinDays:
				node.Status, node.AdmittedDay, node.LastParticipationDay = StatusAdmitted, day, day
			case node.Reactivating:
				node.Status = StatusInactive
			default:
				delete(r, k) // candidature échouée : le nœud peut recandidater avec un parrain
				continue
			}
			node.Reactivating, node.ObservedDays = false, 0
			r[k] = node
		case StatusAdmitted:
			if day-node.LastParticipationDay >= p.InactivityDays {
				node.Status = StatusInactive
				r[k] = node
			}
		}
	}
}

// applySybilExclusion exclut un nœud reconnu comme faux par un vote 2/3
// et prive son parrain de parrainage pendant SponsorBanDays.
// Sera appelée par le module de votes.
func (r registry) applySybilExclusion(addr addrKey, day int64, p Params) error {
	node, ok := r[addr]
	if !ok || node.Status == StatusExcluded {
		return ErrRegistrationStatus
	}
	node.Status = StatusExcluded
	r[addr] = node
	if sponsor, ok := r[node.Sponsor]; ok && node.Sponsor != (addrKey{}) {
		sponsor.SponsorBanUntil = day + p.SponsorBanDays
		r[node.Sponsor] = sponsor
	}
	return nil
}
