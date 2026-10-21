package blockchain

import (
	"bytes"
	"encoding/binary"
	"errors"
	"sort"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// Gouvernance des paramètres : un paramètre gouvernable change par vote de plus de 2/3 des nœuds
// actifs. Le changement est inscrit dans un bloc, mis EN ATTENTE, puis appliqué par tous
// ParamActivationDays jours plus tard — personne n'est pris par surprise.
//
// Garde-fous inscrits dans le code (immuables, même un vote unanime ne peut pas en sortir) :
//   - des bornes absolues par paramètre ;
//   - une variation limitée par vote : frais, frais minimum, taille de bloc et récompense ne peuvent
//     ni plus que doubler ni plus que diviser par deux ; la difficulté ne bouge que de ±2 bits ;
//   - un seul changement en attente par paramètre.
//
// Restent fixes (nouvelle version du logiciel nécessaire) : le calendrier du consensus (23:00,
// fenêtre, tolérances, tours), l'identité du réseau (chain_id, préfixe, genesis) et les règles
// immuables du manifeste.

// ParamID identifie un paramètre gouvernable.
type ParamID uint8

const (
	ParamFeeBps ParamID = iota + 1
	ParamMinFee
	ParamMaxBlockTxs
	ParamDailyReward
	ParamTargetBits
	ParamObservationDays
	ParamObservationMinDays
	ParamInactivityDays
	ParamSponsorSeniorityDays
	ParamSponsorCooldownDays
	ParamSponsorBanDays
	ParamMinActiveNodes
	lastParamID = ParamMinActiveNodes
)

// ParamActivationDays : délai entre l'inscription d'un changement et son application.
const ParamActivationDays = 30

// ParamVoteDays : durée de validité d'un vote de paramètre après sa signature.
const ParamVoteDays = 30

type paramRule struct {
	name     string
	min, max uint64
	halving  bool // variation limitée à ×2 / ÷2 par vote
	maxStep  uint64
}

var paramRules = map[ParamID]paramRule{
	ParamFeeBps:               {"fee_bps", 0, 100, true, 0},                              // 0 à 1 %
	ParamMinFee:               {"min_fee", 0, UnitsPerETL / 100, true, 0},                // 0 à 0,01 ETL
	ParamMaxBlockTxs:          {"max_block_txs", 10_000, 1_000_000, true, 0},             //
	ParamDailyReward:          {"daily_reward", UnitsPerETL, 100 * UnitsPerETL, true, 0}, // 1 à 100 ETL
	ParamTargetBits:           {"target_bits", 16, 28, false, 2},                         // ±2 bits par vote
	ParamObservationDays:      {"observation_days", 3, 30, false, 0},
	ParamObservationMinDays:   {"observation_min_days", 1, 30, false, 0},
	ParamInactivityDays:       {"inactivity_days", 7, 365, false, 0},
	ParamSponsorSeniorityDays: {"sponsor_seniority_days", 30, 730, false, 0},
	ParamSponsorCooldownDays:  {"sponsor_cooldown_days", 7, 365, false, 0},
	ParamSponsorBanDays:       {"sponsor_ban_days", 30, 1825, false, 0},
	ParamMinActiveNodes:       {"min_active_nodes", 3, 100, false, 0},
}

// Name renvoie le nom lisible d'un paramètre.
func (id ParamID) Name() string { return paramRules[id].name }

var (
	ErrParamUnknown   = errors.New("blockchain: paramètre non gouvernable")
	ErrParamBounds    = errors.New("blockchain: valeur hors des bornes immuables du paramètre")
	ErrParamStep      = errors.New("blockchain: variation trop forte pour un seul vote")
	ErrParamPending   = errors.New("blockchain: un changement de ce paramètre est déjà en attente")
	ErrParamRelation  = errors.New("blockchain: valeurs incohérentes (observation minimale > durée d'observation)")
	ErrParamVote      = errors.New("blockchain: vote de paramètre invalide")
	ErrParamVoteDays  = errors.New("blockchain: vote de paramètre expiré ou daté du futur")
	ErrParamUnchanged = errors.New("blockchain: valeur identique à la valeur actuelle")
)

// Get renvoie la valeur d'un paramètre gouvernable.
func (p Params) Get(id ParamID) uint64 {
	switch id {
	case ParamFeeBps:
		return p.FeeBps
	case ParamMinFee:
		return p.MinFee
	case ParamMaxBlockTxs:
		return uint64(p.MaxBlockTxs)
	case ParamDailyReward:
		return p.DailyReward
	case ParamTargetBits:
		return uint64(p.TargetBits)
	case ParamObservationDays:
		return uint64(p.ObservationDays)
	case ParamObservationMinDays:
		return uint64(p.ObservationMinDays)
	case ParamInactivityDays:
		return uint64(p.InactivityDays)
	case ParamSponsorSeniorityDays:
		return uint64(p.SponsorSeniorityDays)
	case ParamSponsorCooldownDays:
		return uint64(p.SponsorCooldownDays)
	case ParamSponsorBanDays:
		return uint64(p.SponsorBanDays)
	case ParamMinActiveNodes:
		return uint64(p.MinActiveNodes)
	}
	return 0
}

// With renvoie une copie des paramètres où id vaut v.
func (p Params) With(id ParamID, v uint64) Params {
	switch id {
	case ParamFeeBps:
		p.FeeBps = v
	case ParamMinFee:
		p.MinFee = v
	case ParamMaxBlockTxs:
		p.MaxBlockTxs = int(v)
	case ParamDailyReward:
		p.DailyReward = v
	case ParamTargetBits:
		p.TargetBits, p.Target = uint(v), TargetFromBits(uint(v))
	case ParamObservationDays:
		p.ObservationDays = int64(v)
	case ParamObservationMinDays:
		p.ObservationMinDays = int(v)
	case ParamInactivityDays:
		p.InactivityDays = int64(v)
	case ParamSponsorSeniorityDays:
		p.SponsorSeniorityDays = int64(v)
	case ParamSponsorCooldownDays:
		p.SponsorCooldownDays = int64(v)
	case ParamSponsorBanDays:
		p.SponsorBanDays = int64(v)
	case ParamMinActiveNodes:
		p.MinActiveNodes = int(v)
	}
	return p
}

// CheckParamChange vérifie qu'un changement respecte bornes et variation maximale par rapport à old.
func CheckParamChange(id ParamID, old, v uint64) error {
	rule, ok := paramRules[id]
	if !ok {
		return ErrParamUnknown
	}
	if v < rule.min || v > rule.max {
		return ErrParamBounds
	}
	if v == old {
		return ErrParamUnchanged
	}
	if rule.halving && (v > max(2*old, 1) || v < old/2) {
		return ErrParamStep
	}
	if rule.maxStep > 0 && (v > old+rule.maxStep || v+rule.maxStep < old) {
		return ErrParamStep
	}
	return nil
}

// PendingParam est un changement voté, en attente d'activation.
type PendingParam struct {
	Value         uint64
	ActivationDay int64 // jour UTC (horodatage ÷ 86 400) à partir duquel il s'applique
}

// --- Votes ---

// ParamVote est la signature d'un nœud pour une nouvelle valeur d'un paramètre.
type ParamVote struct {
	Voter     crypto.Address
	ID        ParamID
	Value     uint64
	Day       int64            // jour de signature : valable ParamVoteDays jours
	Signature crypto.Signature // usage param-vote
}

func paramMessage(id ParamID, v uint64, day int64) []byte {
	b := append([]byte(nil), byte(id))
	b = binary.BigEndian.AppendUint64(b, v)
	return binary.BigEndian.AppendUint64(b, uint64(day))
}

// SignParamVote produit le vote d'un nœud pour id = value, daté du jour day.
func SignParamVote(key *crypto.PrivateKey, id ParamID, value uint64, day int64, p Params) (ParamVote, error) {
	sig, err := key.Sign(p.ChainID, crypto.PurposeParamVote, paramMessage(id, value, day))
	if err != nil {
		return ParamVote{}, err
	}
	return ParamVote{Voter: key.PublicKey().Address(p.AddressHRP), ID: id, Value: value, Day: day, Signature: sig}, nil
}

// Verify contrôle la signature (et que le paramètre est gouvernable).
func (v *ParamVote) Verify(p Params) error {
	if _, ok := paramRules[v.ID]; !ok || v.Voter.HRP != p.AddressHRP ||
		crypto.Verify(v.Voter, p.ChainID, crypto.PurposeParamVote, paramMessage(v.ID, v.Value, v.Day), v.Signature) != nil {
		return ErrParamVote
	}
	return nil
}

// ParamChange est un changement de paramètre voté, inscrit dans un bloc avec ses votes.
type ParamChange struct {
	ID    ParamID
	Value uint64
	Votes []ParamVote // triés par votant, un seul par votant, tous pour (ID, Value)
}

// --- État : paramètres en vigueur et changements en attente ---

// govState : valeurs en vigueur des paramètres gouvernables et changements en attente.
type govState struct {
	values  map[ParamID]uint64
	pending map[ParamID]PendingParam
}

func newGov(p Params) govState {
	g := govState{values: map[ParamID]uint64{}, pending: map[ParamID]PendingParam{}}
	for id := ParamID(1); id <= lastParamID; id++ {
		g.values[id] = p.Get(id)
	}
	return g
}

func (g govState) clone() govState {
	c := govState{values: make(map[ParamID]uint64, len(g.values)), pending: make(map[ParamID]PendingParam, len(g.pending))}
	for k, v := range g.values {
		c.values[k] = v
	}
	for k, v := range g.pending {
		c.pending[k] = v
	}
	return c
}

// activate applique les changements dont le jour d'activation est atteint.
func (g govState) activate(day int64) {
	for id, pp := range g.pending {
		if pp.ActivationDay <= day {
			g.values[id] = pp.Value
			delete(g.pending, id)
		}
	}
}

// apply renvoie base avec les valeurs en vigueur.
func (g govState) apply(base Params) Params {
	for id, v := range g.values {
		base = base.With(id, v)
	}
	return base
}

// ParamsAt renvoie les paramètres en vigueur au jour du bloc d'horodatage blockTime :
// valeurs actuelles et changements en attente déjà activés à cette date.
func (s *State) ParamsAt(base Params, blockTime int64) Params {
	if len(s.gov.values) == 0 { // état sans genesis (tests unitaires)
		return base
	}
	g := s.gov.clone()
	g.activate(dayOf(blockTime))
	return g.apply(base)
}

// PendingParams renvoie les changements en attente d'activation.
func (s *State) PendingParams() map[ParamID]PendingParam {
	out := make(map[ParamID]PendingParam, len(s.gov.pending))
	for k, v := range s.gov.pending {
		out[k] = v
	}
	return out
}

// applyParamChanges valide les changements votés du bloc et les met en attente.
// g doit déjà avoir activé les changements arrivés à échéance ce jour-là.
func (g govState) applyParamChanges(chs []ParamChange, base registry, day int64, p Params) error {
	for i := range chs {
		ch := &chs[i]
		if i > 0 && chs[i-1].ID >= ch.ID {
			return ErrGovernanceOrder
		}
		if _, ok := g.pending[ch.ID]; ok {
			return ErrParamPending
		}
		if err := CheckParamChange(ch.ID, g.values[ch.ID], ch.Value); err != nil {
			return err
		}
		voters := make([]crypto.Address, len(ch.Votes))
		for j := range ch.Votes {
			v := &ch.Votes[j]
			if v.ID != ch.ID || v.Value != ch.Value {
				return ErrParamVote
			}
			if v.Day > day || day-v.Day >= ParamVoteDays {
				return ErrParamVoteDays
			}
			if err := v.Verify(p); err != nil {
				return err
			}
			voters[j] = v.Voter
		}
		if err := checkVoters(voters, base); err != nil {
			return err
		}
		g.pending[ch.ID] = PendingParam{Value: ch.Value, ActivationDay: day + ParamActivationDays}
	}
	// Cohérence des valeurs finales (en vigueur ou en attente).
	final := func(id ParamID) uint64 {
		if pp, ok := g.pending[id]; ok {
			return pp.Value
		}
		return g.values[id]
	}
	if final(ParamObservationMinDays) > final(ParamObservationDays) {
		return ErrParamRelation
	}
	return nil
}

func (g govState) encode() []byte {
	b := []byte{byte(len(g.values))}
	for id := ParamID(1); id <= lastParamID; id++ {
		if v, ok := g.values[id]; ok {
			b = append(b, byte(id))
			b = binary.BigEndian.AppendUint64(b, v)
		}
	}
	ids := make([]int, 0, len(g.pending))
	for id := range g.pending {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	b = append(b, byte(len(ids)))
	for _, id := range ids {
		pp := g.pending[ParamID(id)]
		b = append(b, byte(id))
		b = binary.BigEndian.AppendUint64(b, pp.Value)
		b = binary.BigEndian.AppendUint64(b, uint64(pp.ActivationDay))
	}
	return b
}

// EncodeGov encode les paramètres en vigueur et en attente d'un état (stockage).
func (s *State) EncodeGov() []byte { return s.gov.encode() }

// DecodeGov décode des paramètres encodés par EncodeGov.
func DecodeGov(b []byte) (map[ParamID]uint64, map[ParamID]PendingParam, error) {
	r := &reader{b: b}
	values := map[ParamID]uint64{}
	for n := int(r.u8()); n > 0 && r.err == nil; n-- {
		id := ParamID(r.u8())
		values[id] = r.u64()
	}
	pending := map[ParamID]PendingParam{}
	for n := int(r.u8()); n > 0 && r.err == nil; n-- {
		id := ParamID(r.u8())
		pending[id] = PendingParam{Value: r.u64(), ActivationDay: r.i64()}
	}
	g := govState{values: values, pending: pending}
	if r.done() != nil || !bytes.Equal(g.encode(), b) {
		return nil, nil, ErrDecode
	}
	return values, pending, nil
}

// GovernableParams renvoie les paramètres gouvernables, dans l'ordre de leurs identifiants.
func GovernableParams() []ParamID {
	out := make([]ParamID, 0, lastParamID)
	for id := ParamFeeBps; id <= lastParamID; id++ {
		out = append(out, id)
	}
	return out
}

// Bounds renvoie les bornes immuables d'un paramètre gouvernable.
func (id ParamID) Bounds() (min, max uint64) { return paramRules[id].min, paramRules[id].max }

// ParamByName retrouve un paramètre gouvernable par son nom (« fee_bps »…).
func ParamByName(name string) (ParamID, bool) {
	for id, r := range paramRules {
		if r.name == name {
			return id, true
		}
	}
	return 0, false
}
