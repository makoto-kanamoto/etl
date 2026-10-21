package node

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// GenesisFile est le fichier genesis.json publié avec chaque version. Le bloc genesis en est
// reconstruit, puis son empreinte comparée à celle gravée dans le logiciel (M1).
type GenesisFile struct {
	ChainID   string `json:"chain_id"`
	Message   string `json:"message"`
	Timestamp int64  `json:"timestamp"` // 23:00:00 UTC du jour de lancement
	IPC       struct {
		Value        uint64 `json:"value"` // IPC × 1000
		Period       string `json:"period"`
		SourceSHA256 string `json:"source_sha256"`
	} `json:"ipc"`
	Founders []string `json:"founders"` // adresses bech32m des fondateurs (au moins MinFounders)
}

var ErrGenesisMismatch = errors.New("node : genesis.json ne correspond pas à l'empreinte gravée dans ce logiciel")

// Build reconstruit le bloc genesis pour le réseau n.
func (g *GenesisFile) Build(n Network) (blockchain.Block, error) {
	p := n.Params
	if g.ChainID != p.ChainID || g.Message != n.Message {
		return blockchain.Block{}, fmt.Errorf("node : genesis d'un autre réseau (%q)", g.ChainID)
	}
	sum, err := hex.DecodeString(g.IPC.SourceSHA256)
	if err != nil || len(sum) != 32 {
		return blockchain.Block{}, errors.New("node : genesis : empreinte IPC invalide")
	}
	ipc := blockchain.IPCRecord{Value: g.IPC.Value, Period: g.IPC.Period, SourceSHA256: [32]byte(sum)}
	founders := make([]crypto.Address, len(g.Founders))
	for i, s := range g.Founders {
		if founders[i], err = crypto.ParseAddress(s, p.AddressHRP); err != nil {
			return blockchain.Block{}, fmt.Errorf("node : genesis : fondateur %d : %w", i+1, err)
		}
	}
	return blockchain.Genesis(p, n.Message, ipc, g.Timestamp, founders)
}

// LoadGenesis lit genesis.json et vérifie qu'il est bien celui du réseau n : empreinte gravée pour
// un vrai réseau (refus s'il n'est pas encore lancé), accepté tel quel pour le réseau de test.
func LoadGenesis(path string, n Network) (blockchain.Block, error) {
	if !n.Test && n.GenesisHash == (crypto.Hash{}) {
		return blockchain.Block{}, ErrNotLaunched
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return blockchain.Block{}, err
	}
	var g GenesisFile
	if err := json.Unmarshal(data, &g); err != nil {
		return blockchain.Block{}, fmt.Errorf("node : genesis.json illisible : %w", err)
	}
	b, err := g.Build(n)
	if err != nil {
		return blockchain.Block{}, err
	}
	if !n.Test && b.Header.Hash() != n.GenesisHash {
		return blockchain.Block{}, ErrGenesisMismatch
	}
	return b, nil
}

// NewGenesisFile prépare le genesis d'un réseau (commande « etl genesis », cérémonie de lancement) :
// le jour day à 23:00 UTC, l'IPC de référence et les fondateurs. Renvoie aussi l'empreinte à graver.
func NewGenesisFile(n Network, day time.Time, ipc blockchain.IPCRecord, founders []string) (GenesisFile, crypto.Hash, error) {
	g := GenesisFile{ChainID: n.Params.ChainID, Message: n.Message, Founders: founders,
		Timestamp: time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC).Add(n.Params.BlockTimeOfDay).Unix()}
	g.IPC.Value, g.IPC.Period, g.IPC.SourceSHA256 = ipc.Value, ipc.Period, hex.EncodeToString(ipc.SourceSHA256[:])
	b, err := g.Build(n)
	if err != nil {
		return GenesisFile{}, crypto.Hash{}, err
	}
	return g, b.Header.Hash(), nil
}
