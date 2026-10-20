package network

import (
	"encoding/binary"
	"errors"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// Vérifications SANS contexte, faites par le réseau avant même de passer le message à
// l'application : décodage canonique et signatures. Un échec prouve que l'émetteur ment ou
// est défaillant : déconnexion et bannissement.
// Les vérifications qui dépendent de l'état (jour, registre, solde…) appartiennent au Handler.

var errInvalid = errors.New("network: message invalide")

// EncodeFinalized encode un bloc finalisé et son certificat dans un même message.
func EncodeFinalized(b *blockchain.Block, c *blockchain.Certificate) []byte {
	blk := blockchain.EncodeBlock(b)
	out := binary.BigEndian.AppendUint32(nil, uint32(len(blk)))
	out = append(out, blk...)
	return append(out, blockchain.EncodeCertificate(c)...)
}

// DecodeFinalized décode un bloc finalisé et son certificat.
func DecodeFinalized(p []byte, hrp string) (blockchain.Block, blockchain.Certificate, error) {
	if len(p) < 4 {
		return blockchain.Block{}, blockchain.Certificate{}, errInvalid
	}
	n := binary.BigEndian.Uint32(p)
	if uint64(n) > uint64(len(p)-4) {
		return blockchain.Block{}, blockchain.Certificate{}, errInvalid
	}
	b, err := blockchain.DecodeBlock(p[4:4+n], hrp)
	if err != nil {
		return blockchain.Block{}, blockchain.Certificate{}, errInvalid
	}
	c, err := blockchain.DecodeCertificate(p[4+n:], hrp)
	if err != nil {
		return blockchain.Block{}, blockchain.Certificate{}, errInvalid
	}
	return b, c, nil
}

// checkStateless décode le message et vérifie ce qui peut l'être sans contexte.
func checkStateless(m Message, p blockchain.Params) error {
	hrp := p.AddressHRP
	switch m.Type {
	case MsgTx:
		tx, err := blockchain.DecodeTransaction(m.Payload, hrp)
		if err != nil {
			return errInvalid
		}
		return tx.Verify(p)
	case MsgParticipation:
		// La PoW et la signature dépendent du jour (bloc précédent, heure) : contrôlées par le Handler.
		_, err := blockchain.DecodeParticipation(m.Payload, hrp)
		return err
	case MsgVote:
		v, err := blockchain.DecodeVote(m.Payload, hrp)
		if err != nil {
			return errInvalid
		}
		return v.Verify(p)
	case MsgIPCVote:
		v, err := blockchain.DecodeIPCVote(m.Payload, hrp)
		if err != nil {
			return errInvalid
		}
		return v.Verify(p)
	case MsgExclusionVote:
		v, err := blockchain.DecodeExclusionVote(m.Payload, hrp)
		if err != nil {
			return errInvalid
		}
		return v.Verify(p)
	case MsgParamVote:
		v, err := blockchain.DecodeParamVote(m.Payload, hrp)
		if err != nil {
			return errInvalid
		}
		return v.Verify(p)
	case MsgRegistration:
		reg, err := blockchain.DecodeRegistration(m.Payload, hrp)
		if err != nil {
			return errInvalid
		}
		return reg.VerifySignatures(p)
	case MsgProposal:
		pr, err := blockchain.DecodeProposal(m.Payload, hrp)
		if err != nil {
			return errInvalid
		}
		if err := pr.VerifySignature(p); err != nil {
			return err
		}
		h := pr.Block.Header.Hash()
		return crypto.Verify(pr.Block.Header.Proposer, p.ChainID, crypto.PurposeBlockProposal, h[:], pr.Block.Signature)
	case MsgFinalized:
		b, c, err := DecodeFinalized(m.Payload, hrp)
		if err != nil {
			return err
		}
		if c.Type != blockchain.Precommit || c.Height != b.Header.Height || c.BlockHash != b.Header.Hash() {
			return errInvalid
		}
		return nil
	}
	return ErrFrameType
}
