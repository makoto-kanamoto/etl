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

// GenesisPrevHash engage le genesis sur le chain_id et le message :
// changer l'un ou l'autre change toute la chaîne.
func GenesisPrevHash(chainID, message string) crypto.Hash {
	buf := binary.BigEndian.AppendUint32(nil, uint32(len(chainID)))
	buf = append(buf, chainID...)
	buf = append(buf, message...)
	return crypto.Sum(crypto.ContextGenesis, buf)
}

// Genesis construit le bloc 0 : aucune transaction, aucun participant, aucune récompense, aucun proposant.
// ipc est l'IPC de référence : 1 ETL = 1 unité de cet IPC (règle immuable n°1).
func Genesis(p Params, message string, ipc IPCRecord, timestamp int64) (Block, error) {
	if err := ipc.Validate(); err != nil {
		return Block{}, ErrGenesisIPC
	}
	if !isBlockTime(timestamp, p) {
		return Block{}, ErrBlockTime
	}
	return Block{Header: Header{
		Height:           0,
		PrevHash:         GenesisPrevHash(p.ChainID, message),
		Timestamp:        timestamp,
		TxRoot:           TxRoot(nil),
		ParticipantsRoot: ParticipantsRoot(nil),
		IPC:              &ipc,
		Proposer:         crypto.Address{HRP: p.AddressHRP},
	}}, nil
}
