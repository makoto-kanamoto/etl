package blockchain

import (
	"bytes"
	"encoding/binary"
	"errors"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// Votes de gouvernance inscrits dans les blocs : mise à jour de l'IPC et exclusion Sybil.
// Même seuil que la finalisation : strictement plus de 2/3 des nœuds actifs, comptés au
// moment de l'inclusion (registre AVANT le bloc). Un votant devenu inactif ne compte plus.
//
// Limite assumée du modèle 2/3 : une coalition de plus de 2/3 des nœuds pourrait exclure
// des nœuds honnêtes ou imposer une valeur IPC. C'est la même hypothèse de confiance que
// pour tout le consensus. La protection est la transparence : chaque vote est signé et
// inscrit dans la chaîne — chacun voit qui a voté quoi.

var (
	ErrIPCVotes          = errors.New("blockchain: votes IPC invalides")
	ErrIPCPeriodOrder    = errors.New("blockchain: période IPC non postérieure à la période courante")
	ErrIPCVotesWithout   = errors.New("blockchain: votes IPC sans mise à jour IPC")
	ErrExclusion         = errors.New("blockchain: exclusion invalide")
	ErrExclusionVoteDays = errors.New("blockchain: vote d'exclusion expiré ou daté du futur")
	ErrGovernanceVoter   = errors.New("blockchain: votant non admis")
	ErrGovernanceOrder   = errors.New("blockchain: votes non triés ou en double")
)

// ExclusionVoteDays : durée de validité d'un vote d'exclusion après sa signature.
const ExclusionVoteDays = 30

// --- Vote IPC ---

// IPCVote est la signature d'un nœud pour une valeur IPC lue à la source officielle.
type IPCVote struct {
	Voter     crypto.Address
	Record    IPCRecord
	Signature crypto.Signature // usage ipc-vote
}

func appendIPCRecord(b []byte, r *IPCRecord) []byte {
	b = binary.BigEndian.AppendUint64(b, r.Value)
	b = append(b, byte(len(r.Period)))
	b = append(b, r.Period...)
	return append(b, r.SourceSHA256[:]...)
}

// SignIPCVote produit le vote d'un nœud pour le triplet {valeur, période, sha256}.
func SignIPCVote(key *crypto.PrivateKey, r IPCRecord, p Params) (IPCVote, error) {
	sig, err := key.Sign(p.ChainID, crypto.PurposeIPCVote, appendIPCRecord(nil, &r))
	if err != nil {
		return IPCVote{}, err
	}
	return IPCVote{Voter: key.PublicKey().Address(p.AddressHRP), Record: r, Signature: sig}, nil
}

// Verify contrôle le format du triplet et la signature.
func (v *IPCVote) Verify(p Params) error {
	if v.Record.Validate() != nil || v.Voter.HRP != p.AddressHRP ||
		crypto.Verify(v.Voter, p.ChainID, crypto.PurposeIPCVote, appendIPCRecord(nil, &v.Record), v.Signature) != nil {
		return ErrIPCVotes
	}
	return nil
}

// --- Vote d'exclusion Sybil ---

// ExclusionVote est la signature d'un nœud demandant l'exclusion d'un faux nœud.
type ExclusionVote struct {
	Voter     crypto.Address
	Target    crypto.Address
	Day       int64            // jour de signature : valable ExclusionVoteDays jours
	Signature crypto.Signature // usage exclusion-vote
}

func exclusionMessage(target crypto.Address, day int64) []byte {
	return binary.BigEndian.AppendUint64(append([]byte(nil), target.Hash[:]...), uint64(day))
}

// SignExclusionVote produit un vote d'exclusion de target, daté du jour day.
func SignExclusionVote(key *crypto.PrivateKey, target crypto.Address, day int64, p Params) (ExclusionVote, error) {
	sig, err := key.Sign(p.ChainID, crypto.PurposeExclusionVote, exclusionMessage(target, day))
	if err != nil {
		return ExclusionVote{}, err
	}
	return ExclusionVote{Voter: key.PublicKey().Address(p.AddressHRP), Target: target, Day: day, Signature: sig}, nil
}

// Verify contrôle la signature.
func (v *ExclusionVote) Verify(p Params) error {
	if v.Voter.HRP != p.AddressHRP ||
		crypto.Verify(v.Voter, p.ChainID, crypto.PurposeExclusionVote, exclusionMessage(v.Target, v.Day), v.Signature) != nil {
		return ErrExclusion
	}
	return nil
}

// Exclusion est une exclusion Sybil votée : la cible et les votes qui l'ont décidée.
type Exclusion struct {
	Target crypto.Address
	Votes  []ExclusionVote // triés par votant, un seul par votant, tous pour Target
}

// --- Engagement dans l'en-tête ---

// VotesRoot engage l'en-tête sur les votes IPC, les exclusions et les changements de paramètres du bloc.
func VotesRoot(ipcVotes []IPCVote, exclusions []Exclusion, params []ParamChange) crypto.Hash {
	ipcIDs := make([]crypto.Hash, len(ipcVotes))
	for i := range ipcVotes {
		ipcIDs[i] = crypto.Sum(crypto.ContextRegistry, appendIPCVote([]byte{'I'}, &ipcVotes[i]))
	}
	exIDs := make([]crypto.Hash, len(exclusions))
	for i := range exclusions {
		exIDs[i] = crypto.Sum(crypto.ContextRegistry, appendExclusion([]byte{'X'}, &exclusions[i]))
	}
	pIDs := make([]crypto.Hash, len(params))
	for i := range params {
		pIDs[i] = crypto.Sum(crypto.ContextRegistry, appendParamChange([]byte{'P'}, &params[i]))
	}
	return merkleRoot([]crypto.Hash{merkleRoot(ipcIDs), merkleRoot(exIDs), merkleRoot(pIDs)})
}

// --- Vérification à l'application d'un bloc ---

// checkVoters vérifie qu'une liste de votants est triée, sans doublon, composée de nœuds admis
// dans le registre AVANT le bloc, et qu'elle atteint le seuil.
func checkVoters(voters []crypto.Address, base registry) error {
	for i, v := range voters {
		if i > 0 && bytes.Compare(voters[i-1].Hash[:], v.Hash[:]) >= 0 {
			return ErrGovernanceOrder
		}
		if base[v.Hash].Status != StatusAdmitted {
			return ErrGovernanceVoter
		}
	}
	if len(voters) < Quorum(base.activeCount()) {
		return ErrQuorum
	}
	return nil
}

// applyIPC valide une mise à jour IPC et renvoie le nouvel IPC courant.
func applyIPC(h *Header, votes []IPCVote, current IPCRecord, base registry, p Params) (IPCRecord, error) {
	if h.IPC == nil {
		if len(votes) > 0 {
			return current, ErrIPCVotesWithout
		}
		return current, nil
	}
	rec := *h.IPC
	if err := rec.Validate(); err != nil {
		return current, err
	}
	// Format AAAA-Qn : l'ordre lexicographique est l'ordre chronologique.
	if rec.Period <= current.Period {
		return current, ErrIPCPeriodOrder
	}
	voters := make([]crypto.Address, len(votes))
	for i := range votes {
		if votes[i].Record != rec {
			return current, ErrIPCVotes
		}
		if err := votes[i].Verify(p); err != nil {
			return current, err
		}
		voters[i] = votes[i].Voter
	}
	if err := checkVoters(voters, base); err != nil {
		return current, err
	}
	return rec, nil
}

// applyExclusions valide les exclusions Sybil du bloc et les applique au registre de travail.
func (r registry) applyExclusions(exs []Exclusion, base registry, day int64, p Params) error {
	for i := range exs {
		ex := &exs[i]
		if i > 0 && bytes.Compare(exs[i-1].Target.Hash[:], ex.Target.Hash[:]) >= 0 {
			return ErrGovernanceOrder
		}
		voters := make([]crypto.Address, len(ex.Votes))
		for j := range ex.Votes {
			v := &ex.Votes[j]
			if v.Target != ex.Target {
				return ErrExclusion
			}
			if v.Day > day || day-v.Day >= ExclusionVoteDays {
				return ErrExclusionVoteDays
			}
			if err := v.Verify(p); err != nil {
				return err
			}
			voters[j] = v.Voter
		}
		if err := checkVoters(voters, base); err != nil {
			return err
		}
		if err := r.applySybilExclusion(ex.Target.Hash, day, p); err != nil {
			return ErrExclusion
		}
	}
	return nil
}
