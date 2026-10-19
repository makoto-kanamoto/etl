package blockchain

import (
	"bytes"
	"sort"
	"time"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// Received est une participation reçue par ce nœud, avec son heure de réception locale.
type Received struct {
	Participation Participation
	ReceivedAt    time.Time
}

// Role indique ce qu'une participation représente pour un nœud donné, un jour donné.
type Role uint8

const (
	RoleNone        Role = iota // ignorée : nœud inconnu, inactif, exclu, ou candidat hors observation
	RoleParticipant             // nœud admis : part de récompense et ticket
	RoleObserver                // candidat en observation : compte pour son admission
)

// Role renvoie le rôle d'un nœud pour le jour du bloc à l'heure blockTime.
func (s *State) Role(a crypto.Address, blockTime int64, p Params) Role {
	node := s.nodes[a.Hash]
	day := dayOf(blockTime)
	switch {
	case node.Status == StatusAdmitted:
		return RoleParticipant
	case node.Status == StatusCandidate && day > node.CandidateDay && day <= node.CandidateDay+p.ObservationDays:
		return RoleObserver
	}
	return RoleNone
}

// DayParticipations est le résultat de la fenêtre du jour, prêt à être inclus dans le bloc.
type DayParticipations struct {
	Participants  []Participation // nœuds admis, triés par ticket croissant ; le premier propose
	Observations  []Participation // candidats en observation, triés par adresse
	Equivocations []Equivocation  // preuves de double participation, triées par nœud
}

// Ticket renvoie le ticket de tirage d'un nœud : Blake3(hash du bloc précédent | adresse).
// Il ne dépend ni du nonce ni du contenu du bloc : miner plus longtemps ne sert à rien.
func Ticket(prevHash crypto.Hash, miner crypto.Address) crypto.Hash {
	buf := make([]byte, 0, crypto.HashSize+crypto.AddressHashSize)
	buf = append(buf, prevHash[:]...)
	buf = append(buf, miner.Hash[:]...)
	return crypto.Sum(crypto.ContextTicket, buf)
}

// Eligible trie les participations reçues dans la fenêtre du jour.
//
// Une participation est retenue si :
//   - elle est reçue dans la fenêtre [blockTime, blockTime + SubmissionWindow[ ;
//   - sa PoW et sa signature sont valides pour ce jour ;
//   - son nœud a un rôle ce jour-là (admis ou candidat en observation) ;
//   - son nœud n'a soumis qu'une seule participation. Deux participations différentes
//     produisent une preuve de double participation, incluse dans le bloc : le nœud est exclu.
//     Un doublon réseau identique n'est pas une double participation.
//
// Participants[r] propose au tour r (proposant de secours) : si le bloc du tour 0 n'est pas
// finalisé, Participants[1] propose au tour 1, et ainsi de suite (voir RoundStart).
func Eligible(recv []Received, prev *Header, blockTime int64, p Params, role func(crypto.Address) Role) DayParticipations {
	start := time.Unix(blockTime, 0)
	end := start.Add(p.SubmissionWindow)
	prevHash := prev.Hash()

	first := map[addrKey]Participation{}
	proofs := map[addrKey]Equivocation{}
	for _, r := range recv {
		pt := r.Participation
		if r.ReceivedAt.Before(start) || !r.ReceivedAt.Before(end) {
			continue
		}
		if pt.Verify(prevHash, blockTime, p) != nil || role(pt.Miner) == RoleNone {
			continue
		}
		k := pt.Miner.Hash
		if f, seen := first[k]; !seen {
			first[k] = pt
		} else if _, proven := proofs[k]; !proven && f.ID() != pt.ID() {
			proofs[k] = NewEquivocation(f, pt)
		}
	}

	var out DayParticipations
	for k, pt := range first {
		if _, cheated := proofs[k]; cheated {
			continue
		}
		if role(pt.Miner) == RoleParticipant {
			out.Participants = append(out.Participants, pt)
		} else {
			out.Observations = append(out.Observations, pt)
		}
	}
	for _, e := range proofs {
		out.Equivocations = append(out.Equivocations, e)
	}

	sortByTicket(out.Participants, prevHash)
	sort.Slice(out.Observations, func(i, j int) bool {
		return bytes.Compare(out.Observations[i].Miner.Hash[:], out.Observations[j].Miner.Hash[:]) < 0
	})
	sort.Slice(out.Equivocations, func(i, j int) bool {
		return bytes.Compare(out.Equivocations[i].A.Miner.Hash[:], out.Equivocations[j].A.Miner.Hash[:]) < 0
	})
	return out
}

// sortByTicket trie des participations par ticket croissant, comme l'exige le protocole.
func sortByTicket(pts []Participation, prevHash crypto.Hash) {
	sort.Slice(pts, func(i, j int) bool {
		return Ticket(prevHash, pts[i].Miner).Cmp(Ticket(prevHash, pts[j].Miner)) < 0
	})
}
