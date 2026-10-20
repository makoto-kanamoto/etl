package crypto

import (
	"encoding/binary"
	"errors"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
)

// SignatureSize est la taille d'une signature récupérable : 1 octet de récupération + r (32) + s (32).
const SignatureSize = 65

// Signature est une signature ECDSA secp256k1 récupérable :
// la clé publique du signataire se déduit de la signature et du message.
type Signature [SignatureSize]byte

// Purpose indique ce qui est signé. Une signature produite pour un usage
// n'est jamais valide pour un autre (une transaction ne peut pas passer pour un vote).
type Purpose string

const (
	PurposeTx            Purpose = "tx"
	PurposeParticipation Purpose = "participation"
	PurposeBlockProposal Purpose = "block-proposal"
	PurposeProposal      Purpose = "proposal"
	PurposeRegistration  Purpose = "registration"
	PurposeSponsorship   Purpose = "sponsorship"
	PurposeBlockVote     Purpose = "block-vote"
	PurposeIPCVote       Purpose = "ipc-vote"
	PurposeExclusionVote Purpose = "exclusion-vote"
	PurposeParamVote     Purpose = "param-vote"
	PurposeEscrow        Purpose = "escrow"
)

var (
	ErrEmptyChainID     = errors.New("crypto: chain_id vide")
	ErrEmptyPurpose     = errors.New("crypto: usage de signature vide")
	ErrInvalidSignature = errors.New("crypto: signature invalide")
	ErrSignerMismatch   = errors.New("crypto: la signature ne correspond pas à l'adresse")
)

// signingHash construit l'empreinte réellement signée :
// Blake3(ContextSignature, len|chainID | len|purpose | msg).
// Le chain_id (ex. "ETL-FR") empêche de rejouer une signature sur un autre fork.
func signingHash(chainID string, purpose Purpose, msg []byte) (Hash, error) {
	if chainID == "" {
		return Hash{}, ErrEmptyChainID
	}
	if purpose == "" {
		return Hash{}, ErrEmptyPurpose
	}
	buf := make([]byte, 0, 8+len(chainID)+len(purpose)+len(msg))
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(chainID)))
	buf = append(buf, chainID...)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(purpose)))
	buf = append(buf, purpose...)
	buf = append(buf, msg...)
	return Sum(ContextSignature, buf), nil
}

// Sign signe msg pour le réseau chainID et l'usage purpose.
// Nonce déterministe (RFC 6979) et s normalisé dans la moitié basse (low-S).
func (p *PrivateKey) Sign(chainID string, purpose Purpose, msg []byte) (Signature, error) {
	h, err := signingHash(chainID, purpose, msg)
	if err != nil {
		return Signature{}, err
	}
	var sig Signature
	copy(sig[:], ecdsa.SignCompact(p.k, h[:], true))
	return sig, nil
}

// RecoverPublicKey retrouve la clé publique qui a signé msg.
// Refuse les signatures high-S et celles produites pour une clé non compressée :
// chaque message signé n'a qu'une seule signature valide par clé.
func RecoverPublicKey(chainID string, purpose Purpose, msg []byte, sig Signature) (PublicKey, error) {
	h, err := signingHash(chainID, purpose, msg)
	if err != nil {
		return PublicKey{}, err
	}
	var s secp256k1.ModNScalar
	if overflow := s.SetByteSlice(sig[33:]); overflow || s.IsOverHalfOrder() {
		return PublicKey{}, ErrInvalidSignature
	}
	k, compressed, err := ecdsa.RecoverCompact(sig[:], h[:])
	if err != nil || !compressed {
		return PublicKey{}, ErrInvalidSignature
	}
	return PublicKey{k: k}, nil
}

// Verify vérifie que sig est une signature valide de msg par le détenteur de addr.
func Verify(addr Address, chainID string, purpose Purpose, msg []byte, sig Signature) error {
	pub, err := RecoverPublicKey(chainID, purpose, msg, sig)
	if err != nil {
		return err
	}
	if pub.Address(addr.HRP) != addr {
		return ErrSignerMismatch
	}
	return nil
}
