package api

import (
	"sync"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// ParticipationWindow : nombre de blocs récents examinés pour l'assiduité du nœud.
const ParticipationWindow = 30

// ipcPoint : un IPC adopté par la chaîne (genesis compris), lu dans l'en-tête du bloc qui l'inscrit.
type ipcPoint struct {
	ipcJSON
	Height uint64 `json:"height"`
	Time   int64  `json:"time"`
}

// derived : données recalculées une seule fois par nouveau bloc, jamais à chaque requête.
type derived struct {
	mu     sync.Mutex
	height uint64 // tête pour laquelle supply et participation sont à jour
	valid  bool

	// Historique de l'IPC : construit au premier appel en relisant les en-têtes (conservés à vie,
	// même par un nœud qui élague), puis complété bloc après bloc. Aucun stockage supplémentaire.
	ipc     []ipcPoint
	ipcNext uint64 // prochaine hauteur à lire

	supply uint64

	// Assiduité du nœud sur les ParticipationWindow derniers blocs conservés.
	participated, examined int
	lastReward             uint64 // part touchée au dernier bloc (0 : non récompensé)
	lastRewardKnown        bool
}

type derivedView struct {
	ipc                    []ipcPoint
	supply                 uint64
	participated, examined int
	lastReward             uint64
	lastRewardKnown        bool
}

// derivedNow met à jour les données si la tête a avancé, puis en renvoie une copie.
func (s *Server) derivedNow() derivedView {
	d := &s.dv
	d.mu.Lock()
	defer d.mu.Unlock()
	tip := s.cfg.Chain.Tip()
	for ; d.ipcNext <= tip.Height; d.ipcNext++ {
		h, err := s.cfg.Chain.HeaderAt(d.ipcNext)
		if err != nil {
			break // en-tête illisible : réessayé au prochain appel
		}
		if h.IPC != nil {
			d.ipc = append(d.ipc, ipcPoint{ipcOf(*h.IPC), h.Height, h.Timestamp})
		}
	}
	if !d.valid || d.height != tip.Height {
		s.cfg.Chain.ReadState(func(st *blockchain.State) { d.supply = st.Supply() })
		d.participated, d.examined, d.lastReward, d.lastRewardKnown = 0, 0, 0, false
		if s.cfg.Engine != nil {
			me := s.cfg.Engine.Address()
			for h := tip.Height; h > 0 && d.examined < ParticipationWindow; h-- {
				b, err := s.cfg.Chain.BlockAt(h)
				if err != nil {
					break // contenu élagué
				}
				d.examined++
				in := b.Rewarded(me)
				if in {
					d.participated++
				}
				if h == tip.Height {
					d.lastReward, d.lastRewardKnown = s.rewardOf(&b, me)
				}
			}
		}
		d.height, d.valid = tip.Height, true
	}
	return derivedView{ipc: append([]ipcPoint(nil), d.ipc...), supply: d.supply, participated: d.participated,
		examined: d.examined, lastReward: d.lastReward, lastRewardKnown: d.lastRewardKnown}
}

// rewardOf renvoie la part touchée par a au bloc b (0 s'il n'est pas récompensé), et si la part du
// bloc est connue (blocs appliqués par une version antérieure : inconnue).
func (s *Server) rewardOf(b *blockchain.Block, a crypto.Address) (uint64, bool) {
	share, ok := s.cfg.Chain.RewardAt(b.Header.Height)
	if !ok {
		return 0, false
	}
	if !b.Rewarded(a) {
		return 0, true
	}
	return share, true
}
