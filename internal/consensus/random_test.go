package consensus

import (
	"flag"
	"fmt"
	"os"
	"testing"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/crypto"
	"github.com/makoto-kanamoto/etl/internal/network"
)

// Simulation aléatoire : 10 nœuds dont 3 MALVEILLANTS (le maximum toléré : moins d'un tiers),
// pertes et désordre des messages, coupures et pannes aléatoires, puis retour au calme.
//
//	Invariant de sécurité   vérifié chaque jour : jamais deux blocs finalisés à la même hauteur
//	                        parmi les nœuds honnêtes.
//	Invariant de continuité une fois le réseau rétabli, la chaîne avance.
//
// Les malveillants exécutent le moteur ET trichent : chaque vote qu'ils émettent est doublé d'un
// vote contradictoire envoyé à une autre moitié du réseau ; chaque proposition qu'ils font est
// doublée d'une proposition concurrente (nomination amputée) envoyée à l'autre moitié.
//
// Lancer plus de graines : go test ./internal/consensus -run Randomized -seeds 500

var simSeeds = flag.Int("seeds", 30, "nombre de graines de la simulation aléatoire")

const (
	faultyDays  = 6
	healthyDays = 5
)

func TestRandomizedSafetyAndLiveness(t *testing.T) {
	for seed := 0; seed < *simSeeds; seed++ {
		t.Run(fmt.Sprint(seed), func(t *testing.T) { runRandomized(t, int64(seed)) })
	}
}

func runRandomized(t *testing.T, seed int64) {
	s := newSimSeed(t, seed)
	// Une graine sur deux : relais des votes (détection des tricheurs), sinon vues très
	// partielles (verrous et re-propositions fréquents).
	s.shuffle, s.relay = true, seed%2 == 0
	rng := s.rng
	byz := map[int]bool{}
	for len(byz) < 3 {
		byz[rng.Intn(10)] = true
	}
	var honest []int
	for i := range s.nodes {
		if !byz[i] {
			honest = append(honest, i)
		}
	}
	s.adversary = func(from int, m network.Message) []delivery { return s.cheat(from, m) }
	s.byz = byz

	day := 1
	for ; day <= faultyDays; day++ {
		// Pannes et coupures du jour ; pertes de messages aléatoires.
		s.down, s.group = map[int]bool{}, map[int]int{}
		for _, i := range honest {
			if rng.Float64() < 0.15 {
				s.down[i] = true
			}
		}
		if rng.Float64() < 0.5 {
			for i := range s.nodes {
				s.group[i] = rng.Intn(2)
			}
		}
		lossy := rng.Float64() * 0.4
		s.drop = func(delivery) bool { return rng.Float64() < lossy }
		s.syncLagging(honest)
		s.runDay(day)
		s.safety(honest)
	}

	// Retour au calme (les malveillants continuent de tricher).
	s.down, s.group, s.drop = map[int]bool{}, map[int]int{}, nil
	start := s.maxHeight(honest)
	for ; day <= faultyDays+healthyDays; day++ {
		s.syncLagging(honest)
		s.runDay(day)
		s.safety(honest)
	}
	s.syncLagging(honest)
	s.safety(honest)
	if got := s.maxHeight(honest); got < start+uint64(healthyDays)-1 {
		t.Fatalf("CONTINUITÉ : hauteur %d après %d jours calmes (départ %d)", got, healthyDays, start)
	}
	excluded := 0
	s.nodes[honest[0]].store.ReadState(func(st *blockchain.State) {
		for i := range byz {
			if st.Status(s.nodes[i].wallet.Addr) == blockchain.StatusExcluded {
				excluded++
			}
		}
	})
	stats.add(int(start), faultyDays, int(s.maxHeight(honest)), excluded, s.reproposals)
	t.Logf("jours perturbés : %d blocs / %d jours ; final : %d blocs / %d jours ; malveillants exclus : %d/3 ; re-propositions : %d",
		start, faultyDays, s.maxHeight(honest), faultyDays+healthyDays, excluded, s.reproposals)
}

// stats agrège la simulation : elle doit montrer que pannes et tricheries ont réellement eu lieu.
var stats = &simStats{}

type simStats struct {
	runs, faultyBlocks, faultyDays, blocks, excluded, reproposals int
}

func (st *simStats) add(faultyBlocks, faultyDays, blocks, excluded, reproposals int) {
	st.runs++
	st.faultyBlocks += faultyBlocks
	st.faultyDays += faultyDays
	st.blocks += blocks
	st.excluded += excluded
	st.reproposals += reproposals
}

func TestMain(m *testing.M) {
	flag.Parse()
	code := m.Run()
	if stats.runs > 0 {
		fmt.Printf("SIMULATION : %d graines | jours perturbés : %d blocs sur %d jours | total : %d blocs | "+
			"malveillants exclus : %d/%d | re-propositions de blocs verrouillés : %d\n",
			stats.runs, stats.faultyBlocks, stats.faultyDays, stats.blocks, stats.excluded, 3*stats.runs, stats.reproposals)
	}
	os.Exit(code)
}

// syncLagging rattrape les nœuds honnêtes en retard (ce que ferait la synchronisation réseau).
func (s *sim) syncLagging(honest []int) {
	best := honest[0]
	for _, i := range honest {
		if s.nodes[i].store.Tip().Height > s.nodes[best].store.Tip().Height {
			best = i
		}
	}
	src := s.nodes[best].store
	for _, i := range honest {
		dst := s.nodes[i].store
		for h := dst.Tip().Height + 1; h <= src.Tip().Height; h++ {
			b, err1 := src.BlockAt(h)
			c, err2 := src.CertificateAt(h)
			if err1 != nil || err2 != nil {
				s.t.Fatalf("synchronisation : %v %v", err1, err2)
			}
			if err := dst.Append(&b, &c); err != nil {
				s.t.Fatalf("SÉCURITÉ : le nœud %d refuse le bloc finalisé %d d'un autre nœud honnête : %v", i, h, err)
			}
			s.nodes[i].e.afterFinalize()
		}
	}
}

func (s *sim) maxHeight(nodes []int) uint64 {
	var m uint64
	for _, i := range nodes {
		m = max(m, s.nodes[i].store.Tip().Height)
	}
	return m
}

// halves partage les autres nœuds en deux moitiés aléatoires.
func (s *sim) halves(from int) (a, b []int) {
	for j := range s.nodes {
		if j == from {
			continue
		}
		if s.rng.Intn(2) == 0 {
			a = append(a, j)
		} else {
			b = append(b, j)
		}
	}
	return a, b
}

func to(list []int, from int, m network.Message) []delivery {
	out := make([]delivery, 0, len(list))
	for _, j := range list {
		out = append(out, delivery{from, j, m})
	}
	return out
}

// cheat : ce que diffuse réellement un nœud malveillant à la place d'un message honnête.
func (s *sim) cheat(from int, m network.Message) []delivery {
	key := s.nodes[from].wallet.Key
	a, b := s.halves(from)
	switch m.Type {
	case network.MsgVote:
		v, err := blockchain.DecodeVote(m.Payload, s.p.AddressHRP)
		if err != nil {
			break
		}
		var fake crypto.Hash
		s.rng.Read(fake[:])
		h := blockchain.Header{Height: v.Height, StateRoot: fake}
		fv, err := blockchain.SignVote(key, v.Type, &h, v.Round, s.p)
		if err != nil {
			break
		}
		return append(to(a, from, m), to(b, from, network.Message{Type: network.MsgVote, Payload: blockchain.EncodeVote(&fv)})...)
	case network.MsgProposal:
		pr, err := blockchain.DecodeProposal(m.Payload, s.p.AddressHRP)
		if err == nil && pr.POL != nil {
			s.reproposals++
		}
		if err != nil || pr.POL != nil || len(pr.Nomination) <= int(pr.Round.Round)+1 {
			break
		}
		alt := pr
		alt.Nomination = append([]blockchain.Participation(nil), pr.Nomination[:len(pr.Nomination)-1]...)
		blk := pr.Block
		blk.Participants = alt.Nomination
		ok := false
		s.nodes[from].store.ReadState(func(st *blockchain.State) {
			root, err := st.PreviewRoot(withRoots(&blk), s.p)
			if err == nil {
				blk.Header.StateRoot, ok = root, true
			}
		})
		if !ok || blk.Sign(key, s.p.ChainID) != nil {
			break
		}
		alt.Block = blk
		if alt.Sign(key, s.p) != nil {
			break
		}
		return append(to(a, from, m), to(b, from, network.Message{Type: network.MsgProposal, Payload: blockchain.EncodeProposal(&alt)})...)
	}
	return []delivery{{from, -1, m}}
}
