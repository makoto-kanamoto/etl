package blockchain

import (
	"encoding/binary"
	"errors"
	"regexp"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// BlockVersion est la version de l'encodage des en-têtes.
const BlockVersion = 1

const secondsPerDay = 86400

var (
	ErrBlockHeight        = errors.New("blockchain: hauteur incorrecte")
	ErrBlockPrev          = errors.New("blockchain: hash du bloc précédent incorrect")
	ErrBlockTime          = errors.New("blockchain: horodatage incorrect")
	ErrBlockTxRoot        = errors.New("blockchain: racine des transactions incorrecte")
	ErrBlockPartRoot      = errors.New("blockchain: racine des participations incorrecte")
	ErrBlockNoParticipant = errors.New("blockchain: aucune participation")
	ErrBlockPartOrder     = errors.New("blockchain: participations non triées par ticket croissant")
	ErrBlockNotAdmitted   = errors.New("blockchain: participant non admis")
	ErrBlockProposer      = errors.New("blockchain: le proposant n'a pas le ticket le plus bas")
	ErrBlockRegistryRoot  = errors.New("blockchain: racine du registre incorrecte")
	ErrBlockVotesRoot     = errors.New("blockchain: racine des votes incorrecte")
	ErrBlockFounders      = errors.New("blockchain: fondateurs hors du genesis")
	ErrIPCPeriod          = errors.New("blockchain: période IPC invalide (format AAAA-Qn)")
	ErrIPCValue           = errors.New("blockchain: valeur IPC nulle")
)

// IPCRecord est une valeur IPC validée par vote, gravée dans un bloc.
type IPCRecord struct {
	Value        uint64   // IPC × 1000 (103.07 → 103070)
	Period       string   // "2026-Q3"
	SourceSHA256 [32]byte // SHA256 de l'élément <Obs> de la réponse SDMX INSEE
}

var periodRe = regexp.MustCompile(`^[0-9]{4}-Q[1-4]$`)

// Validate vérifie le format de l'enregistrement IPC.
func (r *IPCRecord) Validate() error {
	if !periodRe.MatchString(r.Period) {
		return ErrIPCPeriod
	}
	if r.Value == 0 {
		return ErrIPCValue
	}
	return nil
}

// Header est l'en-tête d'un bloc. Son hash Blake3 est l'identifiant du bloc.
type Header struct {
	Height           uint64
	PrevHash         crypto.Hash
	Timestamp        int64       // 23:00:00 UTC du jour du bloc, heure Unix
	TxRoot           crypto.Hash // racine de Merkle des identifiants de transactions
	ParticipantsRoot crypto.Hash // racine de Merkle des identifiants de participations
	RegistryRoot     crypto.Hash // racine des enregistrements, observations et preuves de double participation
	VotesRoot        crypto.Hash // racine des votes IPC et des exclusions Sybil
	StateRoot        crypto.Hash // racine de l'état (comptes + registre + IPC) APRÈS application du bloc
	IPC              *IPCRecord  // nil si pas de mise à jour IPC dans ce bloc
	Proposer         crypto.Address
}

// Encode renvoie l'encodage binaire canonique de l'en-tête.
func (h *Header) Encode() []byte {
	b := make([]byte, 0, 256)
	b = append(b, BlockVersion)
	b = binary.BigEndian.AppendUint64(b, h.Height)
	b = append(b, h.PrevHash[:]...)
	b = binary.BigEndian.AppendUint64(b, uint64(h.Timestamp))
	b = append(b, h.TxRoot[:]...)
	b = append(b, h.ParticipantsRoot[:]...)
	b = append(b, h.RegistryRoot[:]...)
	b = append(b, h.VotesRoot[:]...)
	b = append(b, h.StateRoot[:]...)
	if h.IPC == nil {
		b = append(b, 0)
	} else {
		b = append(b, 1)
		b = appendIPCRecord(b, h.IPC)
	}
	return append(b, h.Proposer.Hash[:]...)
}

// Hash renvoie l'identifiant du bloc.
func (h *Header) Hash() crypto.Hash {
	return crypto.Sum(crypto.ContextBlock, h.Encode())
}

// Block est un bloc complet, signé par son proposant.
type Block struct {
	Header       Header
	Participants []Participation // triées par ticket croissant ; la première est celle du proposant
	Txs          []Transaction
	Signature    crypto.Signature // signature du proposant sur le hash de l'en-tête (usage block-proposal)

	// Registre des nœuds
	Registrations     []Registration     // candidatures et réactivations, triées par candidat
	Observations      []Participation    // participations des candidats en observation, triées par adresse
	Equivocations     []Equivocation     // preuves de double participation, triées par nœud
	VoteEquivocations []VoteEquivocation // preuves de double vote, triées par votant

	// Gouvernance
	IPCVotes   []IPCVote        // votes pour Header.IPC, triés par votant (vide si pas de mise à jour)
	Exclusions []Exclusion      // exclusions Sybil votées, triées par cible
	Founders   []crypto.Address // genesis uniquement : fondateurs admis d'office
}

// Sign signe le bloc avec la clé du proposant.
func (b *Block) Sign(key *crypto.PrivateKey, chainID string) error {
	h := b.Header.Hash()
	sig, err := key.Sign(chainID, crypto.PurposeBlockProposal, h[:])
	if err != nil {
		return err
	}
	b.Signature = sig
	return nil
}

// TxRoot calcule la racine de Merkle des identifiants de transactions.
func TxRoot(txs []Transaction) crypto.Hash {
	ids := make([]crypto.Hash, len(txs))
	for i := range txs {
		ids[i] = txs[i].ID
	}
	return merkleRoot(ids)
}

// ParticipantsRoot calcule la racine de Merkle des identifiants de participations.
func ParticipantsRoot(pts []Participation) crypto.Hash {
	ids := make([]crypto.Hash, len(pts))
	for i := range pts {
		ids[i] = pts[i].ID()
	}
	return merkleRoot(ids)
}

// merkleRoot suit la structure RFC 6962 : feuilles et nœuds internes préfixés différemment,
// un nœud impair n'est jamais dupliqué (pas de piège CVE-2012-2459).
func merkleRoot(ids []crypto.Hash) crypto.Hash {
	switch len(ids) {
	case 0:
		return crypto.Sum(crypto.ContextMerkle, nil)
	case 1:
		return crypto.Sum(crypto.ContextMerkle, append([]byte{0x00}, ids[0][:]...))
	}
	k := 1
	for k*2 < len(ids) {
		k *= 2
	}
	l, r := merkleRoot(ids[:k]), merkleRoot(ids[k:])
	buf := make([]byte, 0, 1+2*crypto.HashSize)
	buf = append(buf, 0x01)
	buf = append(buf, l[:]...)
	buf = append(buf, r[:]...)
	return crypto.Sum(crypto.ContextMerkle, buf)
}

// ValidateBlock vérifie un bloc par rapport au précédent, sans toucher à l'état des comptes.
// Vérifications déterministes : tout nœud, à tout moment, aboutit au même résultat.
// (Le contrôle « le bloc inclut toutes les participations que j'ai reçues » relève du vote.)
func ValidateBlock(b *Block, prev *Header, p Params, isAdmitted func(crypto.Address) bool) error {
	h := &b.Header
	prevHash := prev.Hash()
	switch {
	case h.Height != prev.Height+1:
		return ErrBlockHeight
	case h.PrevHash != prevHash:
		return ErrBlockPrev
	case h.Timestamp <= prev.Timestamp || !isBlockTime(h.Timestamp, p):
		return ErrBlockTime
	case h.TxRoot != TxRoot(b.Txs):
		return ErrBlockTxRoot
	case h.ParticipantsRoot != ParticipantsRoot(b.Participants):
		return ErrBlockPartRoot
	case h.RegistryRoot != RegistryRoot(b.Registrations, b.Observations, b.Equivocations, b.VoteEquivocations):
		return ErrBlockRegistryRoot
	case h.VotesRoot != VotesRoot(b.IPCVotes, b.Exclusions):
		return ErrBlockVotesRoot
	case len(b.Founders) > 0:
		return ErrBlockFounders
	case len(b.Participants) == 0:
		return ErrBlockNoParticipant
	}

	// Ordre strictement croissant des tickets : canonique, et un seul ticket par nœud.
	var prevTicket crypto.Hash
	for i := range b.Participants {
		pt := &b.Participants[i]
		if err := pt.Verify(prevHash, h.Timestamp, p); err != nil {
			return err
		}
		if !isAdmitted(pt.Miner) {
			return ErrBlockNotAdmitted
		}
		t := Ticket(prevHash, pt.Miner)
		if i > 0 && t.Cmp(prevTicket) <= 0 {
			return ErrBlockPartOrder
		}
		prevTicket = t
	}

	if h.Proposer != b.Participants[0].Miner {
		return ErrBlockProposer
	}
	hash := h.Hash()
	if err := crypto.Verify(h.Proposer, p.ChainID, crypto.PurposeBlockProposal, hash[:], b.Signature); err != nil {
		return err
	}
	if h.IPC != nil {
		return h.IPC.Validate()
	}
	return nil
}

// isBlockTime vérifie que ts tombe exactement à l'heure du bloc (23:00:00 UTC).
// Un jour sans bloc est permis : la hauteur suit les blocs, pas les jours.
func isBlockTime(ts int64, p Params) bool {
	return ts%secondsPerDay == int64(p.BlockTimeOfDay.Seconds())
}
