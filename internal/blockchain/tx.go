package blockchain

import (
	"encoding/binary"
	"errors"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// TxVersion est la version de l'encodage des transactions.
const TxVersion = 1

// txBodySize = version (1) + from (20) + to (20) + amount, fee, nonce, timestamp (4 × 8).
const txBodySize = 1 + 2*crypto.AddressHashSize + 4*8

var (
	ErrTxVersion    = errors.New("blockchain: version de transaction inconnue")
	ErrTxZeroAmount = errors.New("blockchain: montant nul")
	ErrTxSelf       = errors.New("blockchain: expéditeur et destinataire identiques")
	ErrTxFee        = errors.New("blockchain: frais incorrects")
	ErrTxNetwork    = errors.New("blockchain: adresse d'un autre réseau")
	ErrTxID         = errors.New("blockchain: identifiant de transaction incorrect")
	ErrTxSigner     = errors.New("blockchain: la clé ne correspond pas à l'expéditeur")
)

// Transaction est un transfert d'ETL d'un compte à un autre.
type Transaction struct {
	ID        crypto.Hash      // Blake3(corps | signature)
	From      crypto.Address   // adresse de l'expéditeur
	To        crypto.Address   // adresse du destinataire
	Amount    uint64           // montant en unités (1 ETL = 10^8 unités)
	Fee       uint64           // Amount × fee_bps / 10000
	Nonce     uint64           // = nonce du compte expéditeur : anti-rejeu
	Timestamp int64            // heure Unix de création
	Signature crypto.Signature // signature récupérable de l'expéditeur
}

// Body renvoie l'encodage binaire canonique de la transaction, signature exclue.
// C'est exactement ce qui est signé. Le HRP des adresses n'y figure pas :
// il est implicite (un réseau = un HRP) et déjà couvert par le chain_id de la signature.
func (tx *Transaction) Body() []byte {
	b := make([]byte, 0, txBodySize)
	b = append(b, TxVersion)
	b = append(b, tx.From.Hash[:]...)
	b = append(b, tx.To.Hash[:]...)
	b = binary.BigEndian.AppendUint64(b, tx.Amount)
	b = binary.BigEndian.AppendUint64(b, tx.Fee)
	b = binary.BigEndian.AppendUint64(b, tx.Nonce)
	b = binary.BigEndian.AppendUint64(b, uint64(tx.Timestamp))
	return b
}

// ComputeID calcule l'identifiant : Blake3(corps | signature).
// Les signatures étant uniques (low-S), l'identifiant l'est aussi.
func (tx *Transaction) ComputeID() crypto.Hash {
	return crypto.Sum(crypto.ContextTx, append(tx.Body(), tx.Signature[:]...))
}

// Sign signe la transaction avec la clé de l'expéditeur et remplit Signature et ID.
func (tx *Transaction) Sign(key *crypto.PrivateKey, chainID string) error {
	if key.PublicKey().Address(tx.From.HRP) != tx.From {
		return ErrTxSigner
	}
	sig, err := key.Sign(chainID, crypto.PurposeTx, tx.Body())
	if err != nil {
		return err
	}
	tx.Signature = sig
	tx.ID = tx.ComputeID()
	return nil
}

// Verify contrôle tout ce qui ne dépend pas de l'état des comptes :
// format, frais exacts, réseau, identifiant et signature.
func (tx *Transaction) Verify(p Params) error {
	if tx.Amount == 0 {
		return ErrTxZeroAmount
	}
	if tx.From == tx.To {
		return ErrTxSelf
	}
	if tx.From.HRP != p.AddressHRP || tx.To.HRP != p.AddressHRP {
		return ErrTxNetwork
	}
	fee, err := Fee(tx.Amount, p.FeeBps)
	if err != nil {
		return err
	}
	if tx.Fee != fee {
		return ErrTxFee
	}
	if tx.ID != tx.ComputeID() {
		return ErrTxID
	}
	return crypto.Verify(tx.From, p.ChainID, crypto.PurposeTx, tx.Body(), tx.Signature)
}
