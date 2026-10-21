// Package escrow fournit les outils côté client pour les échanges pair à pair : construire et signer
// les opérations de chaque partie, vérifier des conditions avant de les cosigner, savoir quelles
// signatures manquent, et transmettre une opération partiellement signée sous forme de texte.
// Les règles elles-mêmes sont celles du consensus (internal/blockchain/escrow.go).
package escrow

import (
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// TextPrefix précède une opération transmise entre parties (messagerie, copier-coller) :
// « etl-escrow: » + encodage canonique en base64url sans remplissage. Une altération est
// détectée par les signatures.
const TextPrefix = "etl-escrow:"

var (
	ErrTerms    = errors.New("escrow: conditions non conformes aux règles du protocole")
	ErrRole     = errors.New("escrow: cette clé ne correspond pas au rôle demandé")
	ErrNotOpen  = errors.New("escrow: ce n'est pas une ouverture d'échange")
	ErrExpired  = errors.New("escrow: ouverture périmée ou datée dans le futur")
	ErrUnsigned = errors.New("escrow: signature du vendeur absente ou invalide")
	ErrText     = errors.New("escrow: texte d'opération invalide")
	ErrKind     = errors.New("escrow: type d'opération invalide pour un échange existant")
)

// NewTerms prépare les conditions d'un échange avec les frais exacts en vigueur (p : paramètres du
// prochain bloc). arbiter nul : mode simple, et arbiterFee doit être nul.
func NewTerms(seller, buyer, arbiter crypto.Address, amount, arbiterFee uint64, days uint8, timestamp int64, p blockchain.Params) (blockchain.EscrowTerms, error) {
	fee, err := blockchain.RequiredFee(amount, p)
	if err != nil {
		return blockchain.EscrowTerms{}, ErrTerms
	}
	t := blockchain.EscrowTerms{Seller: seller, Buyer: buyer, Arbiter: arbiter, Amount: amount, ArbiterFee: arbiterFee,
		Fee: fee, Days: days, Timestamp: timestamp}
	return t, checkTerms(&t, p)
}

// checkTerms vérifie ce que le client peut vérifier sans l'état de la chaîne (le solde du vendeur et
// l'admission de l'arbitre sont contrôlés par le consensus).
func checkTerms(t *blockchain.EscrowTerms, p blockchain.Params) error {
	fee, err := blockchain.RequiredFee(t.Amount, p)
	switch {
	case err != nil || t.Amount == 0 || t.Fee != fee || t.Seller == t.Buyer:
		return ErrTerms
	case t.Days < blockchain.EscrowMinDays || t.Days > blockchain.EscrowMaxDays:
		return ErrTerms
	case t.HasArbiter() && (t.Arbiter == t.Seller || t.Arbiter == t.Buyer):
		return ErrTerms
	case !t.HasArbiter() && t.ArbiterFee != 0:
		return ErrTerms
	case t.Seller.HRP != p.AddressHRP || t.Buyer.HRP != p.AddressHRP || (t.HasArbiter() && t.Arbiter.HRP != p.AddressHRP):
		return ErrTerms
	}
	return nil
}

func addressOf(key *crypto.PrivateKey, p blockchain.Params) crypto.Address {
	return key.PublicKey().Address(p.AddressHRP)
}

// Open construit l'ouverture et la signe comme vendeur. Avec un arbitre, elle doit encore être
// transmise à l'arbitre pour cosignature (Cosign) avant d'être soumise.
func Open(key *crypto.PrivateKey, t blockchain.EscrowTerms, p blockchain.Params) (blockchain.EscrowOp, error) {
	if err := checkTerms(&t, p); err != nil {
		return blockchain.EscrowOp{}, err
	}
	if addressOf(key, p) != t.Seller {
		return blockchain.EscrowOp{}, ErrRole
	}
	op := blockchain.EscrowOp{Kind: blockchain.EscrowOpen, Terms: t}
	return op, op.Sign(key, blockchain.SignerSeller, p)
}

// Cosign ajoute la signature de l'arbitre à une ouverture, après avoir vérifié qu'il est bien
// l'arbitre désigné, que les conditions sont conformes, que l'ouverture n'est pas périmée à l'heure
// now et que le vendeur l'a signée. L'interface doit avoir affiché les conditions avant.
func Cosign(key *crypto.PrivateKey, op *blockchain.EscrowOp, now time.Time, p blockchain.Params) error {
	if op.Kind != blockchain.EscrowOpen {
		return ErrNotOpen
	}
	t := &op.Terms
	if err := checkTerms(t, p); err != nil {
		return err
	}
	if !t.HasArbiter() || addressOf(key, p) != t.Arbiter {
		return ErrRole
	}
	// Jour du prochain bloc : c'est lui qui devra inclure l'ouverture (après 23:00, celui du lendemain).
	day := now.Unix() / 86400
	if now.Unix()%86400 >= int64(p.BlockTimeOfDay.Seconds()) {
		day++
	}
	if t.Timestamp > now.Unix() || t.Timestamp/86400+blockchain.EscrowOpenValidityDays <= day {
		return ErrExpired
	}
	if op.Signers != blockchain.SignerSeller || verifySeller(op, p) != nil {
		return ErrUnsigned
	}
	return op.Sign(key, blockchain.SignerArbiter, p)
}

// verifySeller contrôle la signature du vendeur seule (l'arbitre n'a pas encore signé).
func verifySeller(op *blockchain.EscrowOp, p blockchain.Params) error {
	if a, err := op.SignerAddress(blockchain.SignerSeller, p); err != nil || a != op.Terms.Seller {
		return ErrUnsigned
	}
	return nil
}

// Act construit et signe une opération sur un échange existant (« payé », litige, libération,
// remboursement) pour le rôle role. seq est la séquence actuelle de l'échange (état de la chaîne).
func Act(key *crypto.PrivateKey, kind blockchain.EscrowKind, id crypto.Hash, seq uint32, role uint8, p blockchain.Params) (blockchain.EscrowOp, error) {
	if kind <= blockchain.EscrowOpen || kind > blockchain.EscrowRefund {
		return blockchain.EscrowOp{}, ErrKind
	}
	op := blockchain.EscrowOp{Kind: kind, Escrow: id, Seq: seq}
	return op, op.Sign(key, role, p)
}

// AddSignature ajoute la signature de role à une opération reçue d'une autre partie (par exemple
// l'arbitre sur une libération signée par l'acheteur), après avoir vérifié, contre l'état e de
// l'échange, que la clé est bien celle du rôle et que l'opération vise cet échange à sa séquence.
func AddSignature(key *crypto.PrivateKey, op *blockchain.EscrowOp, role uint8, id crypto.Hash, e blockchain.Escrow, p blockchain.Params) error {
	if op.Kind == blockchain.EscrowOpen || op.Escrow != id || op.Seq != e.Seq {
		return ErrTerms
	}
	want := map[uint8]crypto.Address{blockchain.SignerSeller: e.Terms.Seller, blockchain.SignerBuyer: e.Terms.Buyer,
		blockchain.SignerArbiter: e.Terms.Arbiter}[role]
	if addressOf(key, p) != want { // sans arbitre, l'adresse attendue est nulle : aucune clé ne correspond
		return ErrRole
	}
	return op.Sign(key, role, p)
}

// Required renvoie les ensembles de signataires (masques) qui rendraient l'opération kind applicable
// à l'échange e le jour day ; vide si elle est impossible. arbiterActive : l'arbitre n'est pas exclu.
// Sert à l'interface (« il manque la signature de l'arbitre ») ; mêmes règles que le consensus.
func Required(e blockchain.Escrow, kind blockchain.EscrowKind, day int64, arbiterActive bool) []uint8 {
	if e.Outcome != blockchain.EscrowPending {
		return nil
	}
	arb := e.Terms.HasArbiter() && arbiterActive
	var out []uint8
	add := func(ok bool, m uint8) {
		if ok {
			out = append(out, m)
		}
	}
	switch kind {
	case blockchain.EscrowPaid:
		add(!e.Paid && day < e.Deadline, blockchain.SignerBuyer)
	case blockchain.EscrowDispute:
		add(!e.Disputed, blockchain.SignerSeller)
		add(!e.Disputed, blockchain.SignerBuyer)
	case blockchain.EscrowRelease:
		add(true, blockchain.SignerSeller)
		add(arb, blockchain.SignerBuyer|blockchain.SignerArbiter)
	case blockchain.EscrowRefund:
		add(true, blockchain.SignerBuyer)
		add(arb, blockchain.SignerSeller|blockchain.SignerArbiter)
		add(!e.Paid && day >= e.Deadline, blockchain.SignerSeller)
	}
	return out
}

// EncodeText renvoie la forme texte d'une opération, à transmettre à une autre partie.
func EncodeText(op *blockchain.EscrowOp) string {
	return TextPrefix + base64.RawURLEncoding.EncodeToString(blockchain.EncodeEscrowOp(op))
}

// DecodeText relit une opération transmise sous forme de texte (espaces autour tolérés).
func DecodeText(s, hrp string) (blockchain.EscrowOp, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, TextPrefix) {
		return blockchain.EscrowOp{}, ErrText
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(s[len(TextPrefix):])
	if err != nil {
		return blockchain.EscrowOp{}, ErrText
	}
	op, err := blockchain.DecodeEscrowOp(raw, hrp)
	if err != nil {
		return blockchain.EscrowOp{}, ErrText
	}
	return op, nil
}
