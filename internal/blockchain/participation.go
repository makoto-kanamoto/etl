package blockchain

import (
	"context"
	"encoding/binary"
	"errors"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

var (
	ErrParticipationNetwork = errors.New("blockchain: participation d'un autre réseau")
	ErrParticipationPoW     = errors.New("blockchain: preuve de travail invalide")
	ErrMiningCanceled       = errors.New("blockchain: minage interrompu")
)

// Participation prouve qu'un nœud a fait la PoW du jour. C'est son ticket d'entrée :
// elle lui donne une part de la récompense et une chance d'être proposant.
// Elle ne dépend pas du contenu du bloc : un nœud la soumet dès qu'il l'a trouvée,
// le bloc est construit après la fermeture de la fenêtre.
type Participation struct {
	Miner     crypto.Address
	Nonce     uint64
	Signature crypto.Signature // signature du nœud (usage participation)
}

// powMessage = prev_hash | heure du bloc | adresse | nonce.
// Lié au bloc précédent : une PoW ne sert qu'un seul jour.
func powMessage(prevHash crypto.Hash, blockTime int64, miner crypto.Address, nonce uint64) []byte {
	b := make([]byte, 0, crypto.HashSize+8+crypto.AddressHashSize+8)
	b = append(b, prevHash[:]...)
	b = binary.BigEndian.AppendUint64(b, uint64(blockTime))
	b = append(b, miner.Hash[:]...)
	return binary.BigEndian.AppendUint64(b, nonce)
}

// PoWHash renvoie l'empreinte de preuve de travail de la participation.
func (pt *Participation) PoWHash(prevHash crypto.Hash, blockTime int64) crypto.Hash {
	return crypto.Sum(crypto.ContextPoW, powMessage(prevHash, blockTime, pt.Miner, pt.Nonce))
}

// ID identifie la participation (signature comprise). Sert à la racine du bloc
// et à distinguer un doublon réseau d'une double participation.
func (pt *Participation) ID() crypto.Hash {
	b := make([]byte, 0, crypto.AddressHashSize+8+crypto.SignatureSize)
	b = append(b, pt.Miner.Hash[:]...)
	b = binary.BigEndian.AppendUint64(b, pt.Nonce)
	b = append(b, pt.Signature[:]...)
	return crypto.Sum(crypto.ContextParticipation, b)
}

// MineParticipation cherche un nonce valide puis signe la participation.
// S'arrête au premier nonce valide : chercher plus bas ne sert à rien.
func MineParticipation(ctx context.Context, key *crypto.PrivateKey, prevHash crypto.Hash, blockTime int64, p Params) (Participation, error) {
	pt := Participation{Miner: key.PublicKey().Address(p.AddressHRP)}
	for !pt.PoWHash(prevHash, blockTime).MeetsTarget(p.Target) {
		pt.Nonce++
		if pt.Nonce%4096 == 0 && ctx.Err() != nil {
			return Participation{}, ErrMiningCanceled
		}
	}
	sig, err := key.Sign(p.ChainID, crypto.PurposeParticipation, powMessage(prevHash, blockTime, pt.Miner, pt.Nonce))
	if err != nil {
		return Participation{}, err
	}
	pt.Signature = sig
	return pt, nil
}

// Verify contrôle la PoW et la signature. Personne ne peut participer au nom d'un autre.
func (pt *Participation) Verify(prevHash crypto.Hash, blockTime int64, p Params) error {
	if pt.Miner.HRP != p.AddressHRP {
		return ErrParticipationNetwork
	}
	if !pt.PoWHash(prevHash, blockTime).MeetsTarget(p.Target) {
		return ErrParticipationPoW
	}
	return crypto.Verify(pt.Miner, p.ChainID, crypto.PurposeParticipation,
		powMessage(prevHash, blockTime, pt.Miner, pt.Nonce), pt.Signature)
}
