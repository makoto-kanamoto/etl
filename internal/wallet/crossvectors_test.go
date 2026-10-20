package wallet

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// Vecteurs croisés Go/JavaScript : le wallet du navigateur (web/wallet) doit reproduire chacun à
// l'octet près. Les signatures étant déterministes (RFC 6979), un seul fichier prouve les deux sens.
//
//	go test ./internal/wallet/ -run TestCrossVectors -update   régénère le fichier
//	go test ./internal/wallet/                                  vérifie qu'il est à jour
//	node --test web/                                            vérifie le JavaScript

var update = flag.Bool("update", false, "régénère web/wallet/testdata/vectors.json")

const vectorsPath = "../../web/wallet/testdata/vectors.json"

type keyVector struct {
	Private  string `json:"private"`
	Public   string `json:"public"`
	Address  string `json:"address"`
	PhraseFR string `json:"phrase_fr"`
	PhraseEN string `json:"phrase_en"`
}

type sigVector struct {
	Key       int    `json:"key"`
	Purpose   string `json:"purpose"`
	Message   string `json:"message"`
	Signature string `json:"signature"`
}

type txVector struct {
	Key       int    `json:"key"`
	To        string `json:"to"`
	Amount    string `json:"amount"` // décimal : dépasse la précision des nombres JavaScript
	Nonce     string `json:"nonce"`
	Timestamp string `json:"timestamp"`
	FeeBps    string `json:"fee_bps"`
	MinFee    string `json:"min_fee"`
	Fee       string `json:"fee"`
	Body      string `json:"body"`
	Signature string `json:"signature"`
	ID        string `json:"id"`
	Encoded   string `json:"encoded"`
}

type vaultVector struct {
	Passphrase string `json:"passphrase"`
	Key        int    `json:"key"`
	Vault      string `json:"vault"`
}

type vectors struct {
	ChainID      string        `json:"chain_id"`
	HRP          string        `json:"hrp"`
	Keys         []keyVector   `json:"keys"`
	Signatures   []sigVector   `json:"signatures"`
	Transactions []txVector    `json:"transactions"`
	Vaults       []vaultVector `json:"vaults"`
}

// browserVault reproduit, indépendamment du JavaScript, le coffre du navigateur (web/wallet/vault.js) :
// phrase de passe en NFC, PBKDF2-SHA256 + AES-256-GCM, en-tête authentifié. Sel et nonce fixés :
// vecteur reproductible.
func browserVault(t *testing.T, key []byte, pass, addr string, salt, nonce []byte) string {
	t.Helper()
	const iterations = 600_000
	h := []byte("etalon-etl wallet")
	h = binary.BigEndian.AppendUint32(h, 1)
	h = append(append(h, byte(len(addr))), addr...)
	h = append(append(h, byte(len("pbkdf2-sha256"))), "pbkdf2-sha256"...)
	h = binary.BigEndian.AppendUint32(h, iterations)
	h = append(append(h, byte(len(salt))), salt...)
	h = append(append(h, byte(len("aes-256-gcm"))), "aes-256-gcm"...)
	k, err := pbkdf2.Key(sha256.New, norm.NFC.String(pass), salt, iterations, 32)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(k)
	gcm, _ := cipher.NewGCM(block)
	v := map[string]any{
		"version":    1,
		"address":    addr,
		"kdf":        map[string]any{"name": "pbkdf2-sha256", "iterations": iterations, "salt": salt},
		"cipher":     map[string]any{"name": "aes-256-gcm", "nonce": nonce},
		"ciphertext": gcm.Seal(nil, nonce, key, h),
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func buildVectors(t *testing.T) vectors {
	p := blockchain.DefaultParams()
	v := vectors{ChainID: p.ChainID, HRP: p.AddressHRP}

	// Clés : 1, N-1 (bornes de la courbe), deux motifs fixes.
	raws := [][]byte{make([]byte, 32), nil, make([]byte, 32), nil}
	raws[0][31] = 1
	raws[1], _ = hex.DecodeString("fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364140")
	for i := range raws[2] {
		raws[2][i] = byte(i + 1)
	}
	s := sha256.Sum256([]byte("etalon-etl vecteur croisé"))
	raws[3] = s[:]
	var priv []*crypto.PrivateKey
	for _, raw := range raws {
		k, err := crypto.PrivateKeyFromBytes(raw)
		if err != nil {
			t.Fatal(err)
		}
		priv = append(priv, k)
		fr, _ := EncodePhrase(raw, French)
		en, _ := EncodePhrase(raw, English)
		v.Keys = append(v.Keys, keyVector{
			Private:  hex.EncodeToString(raw),
			Public:   hex.EncodeToString(k.PublicKey().Bytes()),
			Address:  k.PublicKey().Address(p.AddressHRP).String(),
			PhraseFR: strings.Join(fr, " "),
			PhraseEN: strings.Join(en, " "),
		})
	}

	// Une signature par usage, réparties sur les clés ; messages vide, court et long.
	purposes := []crypto.Purpose{crypto.PurposeTx, crypto.PurposeParticipation, crypto.PurposeBlockProposal,
		crypto.PurposeProposal, crypto.PurposeRegistration, crypto.PurposeSponsorship, crypto.PurposeBlockVote,
		crypto.PurposeIPCVote, crypto.PurposeExclusionVote, crypto.PurposeParamVote}
	for i, purpose := range purposes {
		msg := bytes.Repeat([]byte{byte(i)}, []int{0, 3, 200}[i%3])
		k := i % len(priv)
		sig, err := priv[k].Sign(p.ChainID, purpose, msg)
		if err != nil {
			t.Fatal(err)
		}
		v.Signatures = append(v.Signatures, sigVector{k, string(purpose), hex.EncodeToString(msg), hex.EncodeToString(sig[:])})
	}

	// Transferts : frais minimum, seuil exact du minimum (0,1 ETL), cas courant, frais votés,
	// montant maximal, horodatage négatif (encodé en complément à deux).
	cases := []struct {
		key, to     int
		amount      uint64
		nonce       uint64
		ts          int64
		feeBps, min uint64
	}{
		{0, 1, 1, 0, 1_800_000_000, 10, 10_000},
		{1, 2, 10_000_000, 7, 1_800_000_000, 10, 10_000},
		{2, 3, blockchain.UnitsPerETL * 3 / 2, 42, 1_793_000_000, 10, 10_000},
		{3, 0, blockchain.UnitsPerETL, 1, 1_800_086_400, 20, 20_000},
		{0, 3, math.MaxUint64, math.MaxUint64, math.MaxInt64, 10_000, 0},
		{1, 0, 123_456_789, 3, -1, 5, 10_000},
	}
	for _, c := range cases {
		pp := p
		pp.FeeBps, pp.MinFee = c.feeBps, c.min
		fee, err := blockchain.RequiredFee(c.amount, pp)
		if err != nil {
			t.Fatal(err)
		}
		from := priv[c.key].PublicKey().Address(p.AddressHRP)
		to := priv[c.to].PublicKey().Address(p.AddressHRP)
		tx := blockchain.Transaction{From: from, To: to, Amount: c.amount, Fee: fee, Nonce: c.nonce, Timestamp: c.ts}
		if err := tx.Sign(priv[c.key], p.ChainID); err != nil {
			t.Fatal(err)
		}
		if err := tx.Verify(pp); err != nil {
			t.Fatal(err)
		}
		u := func(x uint64) string { return strconv.FormatUint(x, 10) }
		v.Transactions = append(v.Transactions, txVector{
			Key: c.key, To: to.String(), Amount: u(c.amount), Nonce: u(c.nonce), Timestamp: strconv.FormatInt(c.ts, 10),
			FeeBps: u(c.feeBps), MinFee: u(c.min), Fee: u(fee),
			Body: hex.EncodeToString(tx.Body()), Signature: hex.EncodeToString(tx.Signature[:]),
			ID: hex.EncodeToString(tx.ID[:]), Encoded: hex.EncodeToString(blockchain.EncodeTransaction(&tx)),
		})
	}

	// Coffres du navigateur, produits par Go : phrase de passe ASCII, et accentuée fournie en NFD
	// (le JavaScript doit l'ouvrir avec la forme NFD comme avec la forme NFC).
	accented := norm.NFD.String("phrase de passe élève été")
	if accented == norm.NFC.String(accented) {
		t.Fatal("prémisse : phrase de passe accentuée en NFD")
	}
	for i, pass := range []string{"correct horse battery staple", accented} {
		salt := bytes.Repeat([]byte{byte(0x10 + i)}, 16)
		nonce := bytes.Repeat([]byte{byte(0x20 + i)}, 12)
		addr := v.Keys[i+2].Address
		v.Vaults = append(v.Vaults, vaultVector{pass, i + 2, browserVault(t, raws[i+2], pass, addr, salt, nonce)})
	}
	return v
}

func TestCrossVectors(t *testing.T) {
	data, err := json.MarshalIndent(buildVectors(t), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if *update {
		if err := os.MkdirAll("../../web/wallet/testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(vectorsPath, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatalf("%v (régénérer avec -update)", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("web/wallet/testdata/vectors.json n'est plus à jour : régénérer avec -update et relancer node --test web/")
	}
}
