// Package mempool est la salle d'attente des transactions entre leur réception et le bloc du jour.
//
// Admission : transaction valide, nonce ni passé ni trop en avance, solde suffisant pour TOUTES
// les transactions en attente de l'expéditeur, première arrivée gagnante à nonce égal (les frais
// sont fixes : pas d'enchère), horodatage cohérent.
// Sélection : premier arrivé, premier servi, dans l'ordre des nonces de chaque compte, avec une
// simulation qui garantit que le bloc s'applique sans erreur.
//
// En mémoire uniquement : au redémarrage, le réseau et les wallets rediffusent.
// Les limites (par compte, totale, âge) sont locales à chaque nœud, pas des règles de consensus.
package mempool

import (
	"container/heap"
	"errors"
	"math/bits"
	"sort"
	"sync"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/crypto"
)

var (
	ErrDuplicate         = errors.New("mempool: transaction déjà en attente")
	ErrNonceTooLow       = errors.New("mempool: nonce déjà utilisé")
	ErrNonceTooHigh      = errors.New("mempool: nonce trop en avance")
	ErrConflict          = errors.New("mempool: une autre transaction occupe déjà ce nonce")
	ErrAccountFull       = errors.New("mempool: trop de transactions en attente pour ce compte")
	ErrPoolFull          = errors.New("mempool: plein")
	ErrInsufficientFunds = errors.New("mempool: solde insuffisant pour l'ensemble des transactions en attente")
	ErrFuture            = errors.New("mempool: transaction datée du futur")
	ErrExpired           = errors.New("mempool: transaction trop ancienne")
)

// Options : limites locales du nœud.
type Options struct {
	MaxPerAccount int           // transactions en attente par compte
	MaxTotal      int           // transactions en attente au total ; au-delà, refus (pas d'éviction)
	MaxAge        time.Duration // expiration, mesurée depuis l'horodatage de la transaction
}

// DefaultOptions : 16 par compte, 200 000 au total, 7 jours.
func DefaultOptions() Options {
	return Options{MaxPerAccount: 16, MaxTotal: 200_000, MaxAge: 7 * 24 * time.Hour}
}

// Accounts donne accès aux comptes du dernier bloc finalisé (storage.Store, blockchain.State).
type Accounts interface {
	Account(crypto.Address) blockchain.Account
}

type addrKey = [crypto.AddressHashSize]byte

type entry struct {
	tx  blockchain.Transaction
	seq uint64 // ordre d'arrivée
}

// Pool est le mempool. Sûr en accès concurrent.
type Pool struct {
	mu       sync.Mutex
	p        blockchain.Params
	opt      Options
	byID     map[crypto.Hash]*entry
	accounts map[addrKey]map[uint64]*entry // expéditeur → nonce → transaction
	seq      uint64
}

// SetParams met à jour les paramètres en vigueur (frais, gouvernés par vote) : à appeler après
// chaque bloc, avant Update, pour que les transactions devenues invalides soient écartées.
func (m *Pool) SetParams(p blockchain.Params) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.p = p
}

// New crée un mempool vide.
func New(p blockchain.Params, opt Options) *Pool {
	return &Pool{p: p, opt: opt, byID: map[crypto.Hash]*entry{}, accounts: map[addrKey]map[uint64]*entry{}}
}

// Len renvoie le nombre de transactions en attente.
func (m *Pool) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.byID)
}

// Has indique si une transaction est en attente.
func (m *Pool) Has(id crypto.Hash) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.byID[id]
	return ok
}

// addCost ajoute le coût d'une transaction (montant + frais) à total, sans dépassement :
// un montant énorme dont le coût « reboucle » ne doit jamais passer pour finançable —
// il ferait échouer tout le bloc du proposant.
func addCost(total uint64, tx *blockchain.Transaction) (uint64, bool) {
	s, c1 := bits.Add64(tx.Amount, tx.Fee, 0)
	s, c2 := bits.Add64(total, s, 0)
	return s, c1 == 0 && c2 == 0
}

// Add admet une transaction ; st est l'état du dernier bloc finalisé, now l'heure locale.
func (m *Pool) Add(tx blockchain.Transaction, st Accounts, now time.Time) error {
	m.mu.Lock()
	p := m.p
	m.mu.Unlock()
	if err := tx.Verify(p); err != nil {
		return err
	}
	ts := time.Unix(tx.Timestamp, 0)
	if ts.After(now.Add(p.ClockTolerance)) {
		return ErrFuture
	}
	if now.Sub(ts) > m.opt.MaxAge {
		return ErrExpired
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[tx.ID]; ok {
		return ErrDuplicate
	}
	acc := st.Account(tx.From)
	queue := m.accounts[tx.From.Hash]
	switch {
	case tx.Nonce < acc.Nonce:
		return ErrNonceTooLow
	case queue[tx.Nonce] != nil:
		return ErrConflict
	case tx.Nonce >= acc.Nonce+uint64(m.opt.MaxPerAccount):
		return ErrNonceTooHigh
	case len(queue) >= m.opt.MaxPerAccount:
		return ErrAccountFull
	case len(m.byID) >= m.opt.MaxTotal:
		return ErrPoolFull
	}
	// Solde : toutes les transactions en attente de l'expéditeur doivent être couvertes
	// (les crédits encore en attente ne comptent pas).
	total, ok := addCost(0, &tx)
	for _, e := range queue {
		if !ok {
			break
		}
		total, ok = addCost(total, &e.tx)
	}
	if !ok || total > acc.Balance {
		return ErrInsufficientFunds
	}

	m.seq++
	e := &entry{tx: tx, seq: m.seq}
	if queue == nil {
		queue = map[uint64]*entry{}
		m.accounts[tx.From.Hash] = queue
	}
	queue[tx.Nonce] = e
	m.byID[tx.ID] = e
	return nil
}

// Select choisit les transactions du bloc : au plus max, horodatées au plus tard à blockTime.
// Premier arrivé, premier servi, dans l'ordre des nonces de chaque compte. Une simulation
// des soldes garantit que la sélection s'applique sans erreur (un crédit reçu plus tôt dans
// le même bloc peut financer une transaction suivante).
func (m *Pool) Select(st Accounts, blockTime int64, max int) []blockchain.Transaction {
	m.mu.Lock()
	defer m.mu.Unlock()

	balances := map[addrKey]uint64{}
	balance := func(a crypto.Address) uint64 {
		if b, ok := balances[a.Hash]; ok {
			return b
		}
		return st.Account(a).Balance
	}
	nextNonce := map[addrKey]uint64{}
	h := &heads{}
	push := func(from crypto.Address, nonce uint64) {
		if e := m.accounts[from.Hash][nonce]; e != nil && e.tx.Timestamp <= blockTime {
			heap.Push(h, e)
		}
	}
	for k, queue := range m.accounts {
		for _, e := range queue { // n'importe quelle entrée donne l'adresse de l'expéditeur
			n := st.Account(e.tx.From).Nonce
			nextNonce[k] = n
			push(e.tx.From, n)
			break
		}
	}

	var out []blockchain.Transaction
	for h.Len() > 0 && len(out) < max {
		e := heap.Pop(h).(*entry)
		tx := &e.tx
		c, ok := addCost(0, tx)
		credited, ok2 := bits.Add64(balance(tx.To), tx.Amount, 0)
		if !ok || ok2 != 0 || balance(tx.From) < c {
			continue // ce compte attendra un prochain bloc : ses nonces suivants aussi
		}
		balances[tx.From.Hash] = balance(tx.From) - c
		balances[tx.To.Hash] = credited
		out = append(out, *tx)
		nextNonce[tx.From.Hash]++
		push(tx.From, nextNonce[tx.From.Hash])
	}
	return out
}

// Update purge le mempool après un bloc finalisé : transactions incluses ou au nonce dépassé,
// expirées, ou que le nouveau solde ne couvre plus (avec toutes celles qui en dépendent).
func (m *Pool) Update(st Accounts, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, queue := range m.accounts {
		var nonces []uint64
		for n := range queue {
			nonces = append(nonces, n)
		}
		sort.Slice(nonces, func(i, j int) bool { return nonces[i] < nonces[j] })

		var acc blockchain.Account
		for _, n := range nonces {
			acc = st.Account(queue[n].tx.From)
			break
		}
		var spent uint64
		broken := false
		for _, n := range nonces {
			e := queue[n]
			tooOld := now.Sub(time.Unix(e.tx.Timestamp, 0)) > m.opt.MaxAge
			// Frais changés par vote : une transaction au montant de frais périmé n'est plus valide.
			stale := e.tx.Verify(m.p) != nil
			if n < acc.Nonce || n >= acc.Nonce+uint64(m.opt.MaxPerAccount) || tooOld || stale || broken {
				m.drop(k, n)
				continue
			}
			var ok bool
			if spent, ok = addCost(spent, &e.tx); !ok || spent > acc.Balance {
				broken = true // celle-ci et toutes les suivantes ne sont plus couvertes
				m.drop(k, n)
			}
		}
		if len(queue) == 0 {
			delete(m.accounts, k)
		}
	}
}

func (m *Pool) drop(k addrKey, nonce uint64) {
	e := m.accounts[k][nonce]
	delete(m.byID, e.tx.ID)
	delete(m.accounts[k], nonce)
}

// heads : tas des prochaines transactions de chaque compte, par ordre d'arrivée.
type heads []*entry

func (h heads) Len() int           { return len(h) }
func (h heads) Less(i, j int) bool { return h[i].seq < h[j].seq }
func (h heads) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *heads) Push(x any)        { *h = append(*h, x.(*entry)) }
func (h *heads) Pop() any {
	old := *h
	e := old[len(old)-1]
	*h = old[:len(old)-1]
	return e
}
