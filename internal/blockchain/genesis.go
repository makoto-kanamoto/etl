package blockchain

import (
	"encoding/binary"
	"errors"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

var ErrGenesisIPC = errors.New("blockchain: le genesis doit contenir l'IPC de référence")

// GenesisMessage est le message gravé dans le bloc genesis d'ETL-FR.
const GenesisMessage = "En 1803, Napoléon ancrait le franc sur l'or pour protéger le pouvoir d'achat des Français. " +
	"Cet étalon a tenu 110 ans. Aujourd'hui nous recréons un étalon — non plus sur l'or, " +
	"mais sur la réalité du pouvoir d'achat des Français, mesurée chaque trimestre par l'INSEE. " +
	"Pas de banque. Pas d'État. Juste la communauté."

// GenesisPrevHash engage le genesis sur le chain_id, le message et la liste des fondateurs :
// changer l'un d'eux change toute la chaîne.
func GenesisPrevHash(chainID, message string, founders []crypto.Address) crypto.Hash {
	buf := binary.BigEndian.AppendUint32(nil, uint32(len(chainID)))
	buf = append(buf, chainID...)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(message)))
	buf = append(buf, message...)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(founders)))
	for _, f := range founders {
		buf = append(buf, f.Hash[:]...)
	}
	return crypto.Sum(crypto.ContextGenesis, buf)
}

// Genesis construit le bloc 0 : aucune transaction, aucun participant, aucune récompense, aucun proposant.
// ipc est l'IPC de référence : 1 ETL = 1 unité de cet IPC (règle immuable n°1).
// founders : au moins MinFounders membres indépendants, annoncés publiquement avant le lancement.
func Genesis(p Params, message string, ipc IPCRecord, timestamp int64, founders []crypto.Address) (Block, error) {
	if err := (registry{}).applyFounders(founders, 0, p); err != nil {
		return Block{}, err
	}
	if err := ipc.Validate(); err != nil {
		return Block{}, ErrGenesisIPC
	}
	if !isBlockTime(timestamp, p) {
		return Block{}, ErrBlockTime
	}
	blk := Block{Founders: founders, Header: Header{
		Height:           0,
		PrevHash:         GenesisPrevHash(p.ChainID, message, founders),
		Timestamp:        timestamp,
		TxRoot:           TxRoot(nil),
		ParticipantsRoot: ParticipantsRoot(nil),
		RegistryRoot:     RegistryRoot(nil, nil, nil),
		IPC:              &ipc,
		Proposer:         crypto.Address{HRP: p.AddressHRP},
	}}
	root, err := NewState().PreviewRoot(&blk, p)
	if err != nil {
		return Block{}, err
	}
	blk.Header.StateRoot = root
	return blk, nil
}
