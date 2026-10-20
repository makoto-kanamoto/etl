package wallet

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// Coffre de clé (fichier JSON, versionné) :
//
//	{ "version": 1, "address": "etl1q…",
//	  "kdf":    { "name": "argon2id", "time": 3, "memory_kib": 65536, "threads": 4, "salt": "…" },
//	  "cipher": { "name": "aes-256-gcm", "nonce": "…" },
//	  "ciphertext": "…" }
//
// La clé privée est chiffrée en AES-256-GCM avec une clé dérivée de la phrase de passe par
// Argon2id. L'en-tête (version, adresse, paramètres, sel) est AUTHENTIFIÉ : modifier l'adresse
// affichée ou affaiblir les paramètres rend le fichier illisible. Les paramètres sont inscrits dans
// le fichier pour pouvoir être renforcés plus tard sans casser les anciens coffres.
//
// La phrase de passe est normalisée en NFC avant usage : « é » tapé en un caractère (macOS, Windows)
// ou en deux (e + accent combinant, certains claviers ou copier-coller) déverrouille le même coffre.
// Le coffre du navigateur (web/wallet/vault.js) applique la même règle.

const (
	fileVersion   = 1
	MinPassphrase = 12 // caractères (après NFC) : la longueur compte plus que les symboles

	defaultTime    = 3
	defaultMemory  = 64 * 1024 // Kio : 64 Mio
	defaultThreads = 4

	// Bornes acceptées à la lecture : jamais plus faibles que les valeurs par défaut,
	// jamais démesurées (un fichier hostile ne doit pas épuiser la mémoire).
	maxTime    = 64
	maxMemory  = 4 * 1024 * 1024 // 4 Gio
	maxThreads = 64
)

var (
	ErrPassphraseShort = errors.New("wallet: phrase de passe trop courte (12 caractères minimum)")
	ErrWrongPassphrase = errors.New("wallet: phrase de passe incorrecte ou coffre altéré")
	ErrExists          = errors.New("wallet: un coffre existe déjà à cet emplacement (jamais écrasé)")
	ErrFormat          = errors.New("wallet: coffre illisible ou paramètres refusés")
)

type kdfParams struct {
	Name      string `json:"name"`
	Time      uint32 `json:"time"`
	MemoryKiB uint32 `json:"memory_kib"`
	Threads   uint8  `json:"threads"`
	Salt      []byte `json:"salt"`
}

type cipherParams struct {
	Name  string `json:"name"`
	Nonce []byte `json:"nonce"`
}

type keyFile struct {
	Version    int          `json:"version"`
	Address    string       `json:"address"`
	KDF        kdfParams    `json:"kdf"`
	Cipher     cipherParams `json:"cipher"`
	Ciphertext []byte       `json:"ciphertext"`
}

// header renvoie les données authentifiées (AAD) : tout sauf le nonce et le texte chiffré.
func (f *keyFile) header() []byte {
	b := []byte("etalon-etl wallet")
	b = binary.BigEndian.AppendUint32(b, uint32(f.Version))
	b = append(b, byte(len(f.Address)))
	b = append(b, f.Address...)
	b = append(b, byte(len(f.KDF.Name)))
	b = append(b, f.KDF.Name...)
	b = binary.BigEndian.AppendUint32(b, f.KDF.Time)
	b = binary.BigEndian.AppendUint32(b, f.KDF.MemoryKiB)
	b = append(b, f.KDF.Threads, byte(len(f.KDF.Salt)))
	b = append(b, f.KDF.Salt...)
	b = append(b, byte(len(f.Cipher.Name)))
	return append(b, f.Cipher.Name...)
}

func (f *keyFile) aead(passphrase string) (cipher.AEAD, error) {
	k := f.KDF
	if f.Version != fileVersion || k.Name != "argon2id" || f.Cipher.Name != "aes-256-gcm" ||
		k.Time < defaultTime || k.Time > maxTime || k.MemoryKiB < defaultMemory || k.MemoryKiB > maxMemory ||
		k.Threads < 1 || k.Threads > maxThreads || len(k.Salt) != 16 || len(f.Cipher.Nonce) != 12 {
		return nil, ErrFormat
	}
	key := argon2.IDKey([]byte(norm.NFC.String(passphrase)), k.Salt, k.Time, k.MemoryKiB, k.Threads, 32)
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Wallet est un coffre déverrouillé : la clé privée est en mémoire jusqu'à Close.
type Wallet struct {
	key     *crypto.PrivateKey
	Address crypto.Address
}

// Close efface la clé privée de la mémoire. Le wallet est inutilisable ensuite.
func (w *Wallet) Close() {
	if w.key != nil {
		w.key.Zero()
		w.key = nil
	}
}

// Key renvoie la clé privée (pour le moteur de consensus du nœud). Ne jamais la journaliser.
func (w *Wallet) Key() *crypto.PrivateKey { return w.key }

// Transfer construit et signe un transfert : frais exacts selon les paramètres en vigueur p
// (ceux du prochain bloc : ils peuvent avoir changé par vote).
func (w *Wallet) Transfer(to crypto.Address, amount, nonce uint64, timestamp int64, p blockchain.Params) (blockchain.Transaction, error) {
	fee, err := blockchain.RequiredFee(amount, p)
	if err != nil {
		return blockchain.Transaction{}, err
	}
	tx := blockchain.Transaction{From: w.Address, To: to, Amount: amount, Fee: fee, Nonce: nonce, Timestamp: timestamp}
	if err := tx.Sign(w.key, p.ChainID); err != nil {
		return blockchain.Transaction{}, err
	}
	return tx, tx.Verify(p)
}

// Create génère une nouvelle clé, l'enregistre chiffrée et renvoie la phrase de récupération (24 mots).
func Create(path, passphrase string, lang Language, hrp string) (*Wallet, []string, error) {
	if err := checkPassphrase(passphrase); err != nil {
		return nil, nil, err
	}
	key, err := crypto.GenerateKey()
	if err != nil {
		return nil, nil, err
	}
	raw := key.Bytes()
	defer clear(raw)
	phrase, err := EncodePhrase(raw, lang)
	if err != nil {
		return nil, nil, err
	}
	w, err := save(path, key, passphrase, hrp)
	if err != nil {
		key.Zero()
		return nil, nil, err
	}
	return w, phrase, nil
}

// Restore recrée le coffre à partir de la phrase de récupération.
func Restore(path, phrase, passphrase, hrp string) (*Wallet, error) {
	if err := checkPassphrase(passphrase); err != nil {
		return nil, err
	}
	raw, _, err := DecodePhrase(phrase)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	key, err := crypto.PrivateKeyFromBytes(raw)
	if err != nil {
		return nil, err
	}
	w, err := save(path, key, passphrase, hrp)
	if err != nil {
		key.Zero()
	}
	return w, err
}

// Open déverrouille le coffre.
func Open(path, passphrase, hrp string) (*Wallet, error) {
	f, err := readFile(path, hrp)
	if err != nil {
		return nil, err
	}
	gcm, err := f.aead(passphrase)
	if err != nil {
		return nil, err
	}
	raw, err := gcm.Open(nil, f.Cipher.Nonce, f.Ciphertext, f.header())
	if err != nil {
		return nil, ErrWrongPassphrase
	}
	defer clear(raw)
	key, err := crypto.PrivateKeyFromBytes(raw)
	if err != nil {
		return nil, ErrFormat
	}
	// L'adresse stockée est authentifiée (en-tête) : elle correspond forcément à la clé déchiffrée.
	return &Wallet{key: key, Address: key.PublicKey().Address(hrp)}, nil
}

// ReadAddress renvoie l'adresse du coffre sans phrase de passe (elle est stockée en clair, authentifiée).
func ReadAddress(path, hrp string) (crypto.Address, error) {
	f, err := readFile(path, hrp)
	if err != nil {
		return crypto.Address{}, err
	}
	return crypto.ParseAddress(f.Address, hrp)
}

// ChangePassphrase rechiffre le coffre avec une nouvelle phrase de passe (écriture atomique).
func ChangePassphrase(path, old, new, hrp string) error {
	if err := checkPassphrase(new); err != nil {
		return err
	}
	w, err := Open(path, old, hrp)
	if err != nil {
		return err
	}
	defer w.Close()
	return write(path, w.key, new, hrp, true)
}

func checkPassphrase(p string) error {
	if utf8.RuneCountInString(norm.NFC.String(p)) < MinPassphrase {
		return ErrPassphraseShort
	}
	return nil
}

func save(path string, key *crypto.PrivateKey, passphrase, hrp string) (*Wallet, error) {
	if err := write(path, key, passphrase, hrp, false); err != nil {
		return nil, err
	}
	return &Wallet{key: key, Address: key.PublicKey().Address(hrp)}, nil
}

// write chiffre la clé et l'écrit de façon atomique (fichier temporaire, synchronisation, renommage) :
// une coupure ne laisse jamais un coffre à moitié écrit. Sans replace, refuse d'écraser un coffre existant.
func write(path string, key *crypto.PrivateKey, passphrase, hrp string, replace bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f := keyFile{Version: fileVersion, Address: key.PublicKey().Address(hrp).String(),
		KDF:    kdfParams{Name: "argon2id", Time: defaultTime, MemoryKiB: defaultMemory, Threads: defaultThreads, Salt: random(16)},
		Cipher: cipherParams{Name: "aes-256-gcm", Nonce: random(12)}}
	gcm, err := f.aead(passphrase)
	if err != nil {
		return err
	}
	raw := key.Bytes()
	defer clear(raw)
	f.Ciphertext = gcm.Seal(nil, f.Cipher.Nonce, raw, f.header())
	data, err := json.MarshalIndent(&f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".wallet-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // sans effet après le renommage
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if !replace {
		// Liaison exclusive : échoue si un coffre existe déjà, même apparu entre-temps (jamais d'écrasement).
		if err := os.Link(tmp.Name(), path); err != nil {
			if os.IsExist(err) {
				return ErrExists
			}
			return err
		}
		return nil
	}
	return os.Rename(tmp.Name(), path)
}

func readFile(path, hrp string) (*keyFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f keyFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, ErrFormat
	}
	if _, err := crypto.ParseAddress(f.Address, hrp); err != nil {
		return nil, ErrFormat
	}
	return &f, nil
}

func random(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("wallet: aléa système indisponible")
	}
	return b
}
