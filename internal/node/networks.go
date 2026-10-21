// Package node assemble un nœud ETL : stockage, réseau, consensus, oracle IPC et dashboard.
package node

import (
	"errors"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/crypto"
	"github.com/makoto-kanamoto/etl/internal/network"
)

// Network est la définition d'un réseau, gravée dans le logiciel : paramètres initiaux (ensuite
// gouvernés par vote on-chain), message du genesis, empreinte du genesis et points de contrôle.
type Network struct {
	Name        string
	Params      blockchain.Params
	Message     string
	GenesisHash crypto.Hash          // empreinte gravée à la cérémonie de lancement ; nulle : pas encore lancé
	Checkpoints []network.Checkpoint // blocs connus de cette version (à remplir à chaque version)
	Test        bool                 // réseau de test : accepte tout genesis, jamais confondu avec un vrai réseau
}

// ErrNotLaunched : le réseau n'a pas encore de genesis gravé dans ce logiciel.
var ErrNotLaunched = errors.New("node : ce réseau n'est pas encore lancé (empreinte du genesis absente de cette version)")

// ErrUnknownNetwork : nom de réseau inconnu dans la configuration.
var ErrUnknownNetwork = errors.New("node : réseau inconnu (ETL-FR, ou « test » pour un réseau de test)")

// etlFR : réseau de référence. L'empreinte du genesis sera gravée ici à la cérémonie de lancement
// (date et fondateurs à décider) ; d'ici là, le logiciel refuse de rejoindre ETL-FR.
var etlFR = Network{
	Name:    "ETL-FR",
	Params:  blockchain.DefaultParams(),
	Message: blockchain.GenesisMessage,
}

// TestParams : réseau de test local — 4 fondateurs suffisent, preuve de travail rapide, identifiant
// et préfixe d'adresse distincts (aucune signature ni adresse ne peut être confondue avec ETL-FR).
func TestParams() blockchain.Params {
	p := blockchain.DefaultParams()
	p.ChainID, p.AddressHRP = "ETL-TEST", "etlt"
	p.TargetBits, p.Target = 16, blockchain.TargetFromBits(16)
	p.MinFounders, p.MinActiveNodes = 4, 3
	return p
}

var testNet = Network{Name: "test", Params: TestParams(), Message: "Réseau de test Étalon — sans valeur.", Test: true}

// Lookup renvoie la définition d'un réseau par son nom (node.network dans la configuration).
func Lookup(name string) (Network, error) {
	switch name {
	case etlFR.Name:
		return etlFR, nil
	case testNet.Name:
		return testNet, nil
	}
	return Network{}, ErrUnknownNetwork
}
