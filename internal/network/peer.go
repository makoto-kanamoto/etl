package network

import (
	"net"
	"sync"
	"time"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// sendQueue : messages en attente d'envoi par pair. Un pair trop lent pour suivre est déconnecté.
const sendQueue = 1024

type peer struct {
	host     *Host
	conn     net.Conn
	inbound  bool
	dialAddr string

	out      chan Message
	done     chan struct{}
	once     sync.Once
	limiter  *tokenBucket
	services uint8

	mu      sync.Mutex
	pending map[uint64]chan Message // requêtes de synchronisation en attente de réponse
}

func newPeer(h *Host, conn net.Conn, inbound bool) *peer {
	return &peer{
		host: h, conn: conn, inbound: inbound,
		out: make(chan Message, sendQueue), done: make(chan struct{}), pending: map[uint64]chan Message{},
		limiter: newTokenBucket(h.cfg.RatePerSecond, h.cfg.RateBurst),
	}
}

func (p *peer) isClosed() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func (p *peer) start() {
	go p.readLoop()
	go p.writeLoop()
}

func (p *peer) close() {
	p.once.Do(func() {
		close(p.done)
		p.conn.Close()
		p.host.removePeer(p)
	})
}

// send met un message en file ; si la file est pleine, le pair est trop lent : déconnexion.
func (p *peer) send(m Message) {
	select {
	case p.out <- m:
	case <-p.done:
	default:
		p.close()
	}
}

func (p *peer) writeLoop() {
	ping := time.NewTicker(p.host.cfg.PingInterval)
	defer ping.Stop()
	for {
		var m Message
		select {
		case <-p.done:
			return
		case m = <-p.out:
		case <-ping.C:
			m = Message{Type: MsgPing}
		}
		p.conn.SetWriteDeadline(time.Now().Add(p.host.cfg.HandshakeTimeout))
		if err := writeFrame(p.conn, m); err != nil {
			p.close()
			return
		}
	}
}

func (p *peer) readLoop() {
	defer p.close()
	for {
		p.conn.SetReadDeadline(time.Now().Add(p.host.cfg.IdleTimeout))
		m, err := readFrame(p.conn, p.host.cfg.MaxMessageSize)
		if err != nil {
			if err == ErrFrameTooLarge || err == ErrFrameType {
				p.host.ban(p)
			}
			return
		}
		if !p.limiter.allow() {
			p.host.ban(p) // inondation
			return
		}
		switch {
		case m.Type == MsgPing:
			p.send(Message{Type: MsgPong})
		case m.Type == MsgPong:
		case m.Type == MsgGetAddrs:
			p.send(Message{Type: MsgAddrs, Payload: encodeAddrs(p.host.addrsForPeer())})
		case m.Type == MsgAddrs:
			addrs, err := decodeAddrs(m.Payload)
			if err != nil {
				p.host.ban(p)
				return
			}
			p.host.learnAddrs(addrs)
		case m.Type.IsGossip():
			p.host.onGossip(p, m)
		case m.Type == MsgGetTip || m.Type == MsgGetBlocks:
			go p.host.serve(p, m)
		case m.Type.isResponse():
			p.deliver(m)
		default: // un second Hello, par exemple
			p.host.ban(p)
			return
		}
	}
}

// tokenBucket limite le débit de messages d'un pair.
type tokenBucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newTokenBucket(rate, burst float64) *tokenBucket {
	return &tokenBucket{rate: rate, burst: burst, tokens: burst, last: time.Now()}
}

func (b *tokenBucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.tokens = min(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// seenCache retient les messages déjà vus pendant ttl (dédoublonnage du relais).
type seenCache struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[crypto.Hash]time.Time
}

func newSeenCache(ttl time.Duration) *seenCache {
	return &seenCache{ttl: ttl, m: map[crypto.Hash]time.Time{}}
}

// add renvoie true si le message est nouveau.
func (c *seenCache) add(id crypto.Hash) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t, ok := c.m[id]; ok && time.Since(t) < c.ttl {
		return false
	}
	c.m[id] = time.Now()
	return true
}

func (c *seenCache) sweep() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, t := range c.m {
		if time.Since(t) >= c.ttl {
			delete(c.m, id)
		}
	}
}
