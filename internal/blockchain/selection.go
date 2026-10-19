package blockchain

import (
	"sort"
	"time"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// Received est une participation reçue par ce nœud, avec son heure de réception locale.
type Received struct {
	Participation Participation
	ReceivedAt    time.Time
}

// Ticket renvoie le ticket de tirage d'un nœud : Blake3(hash du bloc précédent | adresse).
// Il ne dépend ni du nonce ni du contenu du bloc : miner plus longtemps ne sert à rien.
func Ticket(prevHash crypto.Hash, miner crypto.Address) crypto.Hash {
	buf := make([]byte, 0, crypto.HashSize+crypto.AddressHashSize)
	buf = append(buf, prevHash[:]...)
	buf = append(buf, miner.Hash[:]...)
	return crypto.Sum(crypto.ContextTicket, buf)
}

// Eligible renvoie les participations éligibles du jour, triées par ticket croissant.
// La première désigne le proposant ; si son bloc n'est pas accepté, la suivante prend le relais.
// Cette liste est aussi celle que le proposant inclut dans son bloc : chacun reçoit une part.
//
// Une participation est éligible si :
//   - elle est reçue dans la fenêtre [blockTime, blockTime + SubmissionWindow[ ;
//   - sa PoW et sa signature sont valides pour ce jour ;
//   - son nœud est admis (isAdmitted) ;
//   - son nœud n'a soumis qu'une seule participation. Deux participations différentes
//     du même nœud l'excluent du jour (un doublon réseau identique n'est pas une double soumission).
func Eligible(recv []Received, prev *Header, blockTime int64, p Params, isAdmitted func(crypto.Address) bool) []Participation {
	start := time.Unix(blockTime, 0)
	end := start.Add(p.SubmissionWindow)
	prevHash := prev.Hash()

	byMiner := map[addrKey]Participation{}
	excluded := map[addrKey]bool{}
	for _, r := range recv {
		pt := r.Participation
		if r.ReceivedAt.Before(start) || !r.ReceivedAt.Before(end) {
			continue
		}
		if pt.Verify(prevHash, blockTime, p) != nil || !isAdmitted(pt.Miner) {
			continue
		}
		k := pt.Miner.Hash
		if first, seen := byMiner[k]; seen {
			if first.ID() != pt.ID() {
				excluded[k] = true
			}
			continue
		}
		byMiner[k] = pt
	}

	out := make([]Participation, 0, len(byMiner))
	tickets := map[addrKey]crypto.Hash{}
	for k, pt := range byMiner {
		if !excluded[k] {
			out = append(out, pt)
			tickets[k] = Ticket(prevHash, pt.Miner)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return tickets[out[i].Miner.Hash].Cmp(tickets[out[j].Miner.Hash]) < 0
	})
	return out
}
