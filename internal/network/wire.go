// Package network relie les nœuds ETL : connexions TCP chiffrées (TLS 1.3), gestion des pairs,
// diffusion des messages par inondation avec dédoublonnage.
//
// Bibliothèque standard uniquement : aucune dépendance réseau tierce à auditer.
// L'identité de transport est jetable et sans lien avec l'adresse ETL du nœud : les pairs voient
// une IP, jamais l'adresse ETL qui s'y cache (pseudonymité, règle n°5). Tous les messages de
// consensus sont de toute façon signés par la clé ETL de leur auteur.
//
// Aucun message n'est relayé sans validation : le réseau ne sert pas d'amplificateur.
package network

import (
	"encoding/binary"
	"errors"
	"io"
	"net"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// ProtocolVersion est la version du protocole réseau.
const ProtocolVersion = 1

// MsgType est le type d'un message.
type MsgType uint8

const (
	// Point à point
	MsgHello MsgType = iota + 1
	MsgPing
	MsgPong
	MsgGetAddrs
	MsgAddrs

	// Diffusés (relayés à tous les pairs après validation)
	MsgTx
	MsgParticipation
	MsgVote
	MsgIPCVote
	MsgExclusionVote
	MsgRegistration // candidature (ou réactivation) d'un nœud
	MsgParamVote    // vote de changement d'un paramètre gouvernable
	MsgEscrowOp     // opération sur un échange sous escrow
	MsgProposal     // bloc proposé, en attente de votes
	MsgFinalized    // bloc finalisé + certificat

	// Synchronisation (requête → réponse, point à point)
	MsgGetTip
	MsgTip
	MsgGetBlocks
	MsgBlocks
	MsgNotAvailable

	lastMsgType = MsgNotAvailable
)

// Services annoncés dans la poignée de main.
const (
	ServiceArchive uint8 = 1 << iota // garde tous les blocs : peut servir une synchronisation depuis le genesis
)

// IsGossip indique si un type de message est diffusé à tout le réseau.
func (t MsgType) IsGossip() bool { return t >= MsgTx && t <= MsgFinalized }

// isResponse indique une réponse à une requête de synchronisation.
func (t MsgType) isResponse() bool { return t == MsgTip || t == MsgBlocks || t == MsgNotAvailable }

var (
	ErrFrameTooLarge = errors.New("network: message trop volumineux")
	ErrFrameType     = errors.New("network: type de message inconnu")
	ErrHello         = errors.New("network: poignée de main invalide")
	ErrWrongNetwork  = errors.New("network: pair d'un autre réseau")
	ErrSelfConnect   = errors.New("network: connexion à soi-même")
)

// Message est une unité d'échange : un type et un contenu encodé (codec canonique de blockchain).
type Message struct {
	Type    MsgType
	Payload []byte
}

// ID identifie un message diffusé, pour le dédoublonnage.
func (m Message) ID() crypto.Hash {
	return crypto.Sum(crypto.ContextMessage, append([]byte{byte(m.Type)}, m.Payload...))
}

// writeFrame écrit [type u8][longueur u32][contenu].
func writeFrame(w io.Writer, m Message) error {
	hdr := make([]byte, 5)
	hdr[0] = byte(m.Type)
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(m.Payload)))
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(m.Payload)
	return err
}

// readFrame lit un message ; refuse un type inconnu ou une taille au-delà de max
// AVANT d'allouer quoi que ce soit.
func readFrame(r io.Reader, max int) (Message, error) {
	hdr := make([]byte, 5)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return Message{}, err
	}
	t := MsgType(hdr[0])
	if t < MsgHello || t > lastMsgType {
		return Message{}, ErrFrameType
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if uint64(n) > uint64(max) {
		return Message{}, ErrFrameTooLarge
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return Message{}, err
	}
	return Message{Type: t, Payload: payload}, nil
}

// hello est le premier message de chaque côté : même protocole, même réseau, même genesis.
type hello struct {
	Version    uint16
	ChainID    string
	Genesis    crypto.Hash
	ListenPort uint16 // 0 : n'accepte pas de connexions entrantes
	Nonce      uint64 // détecte une connexion à soi-même
	Services   uint8  // ServiceArchive…
}

func (h hello) encode() []byte {
	b := binary.BigEndian.AppendUint16(nil, h.Version)
	b = append(b, byte(len(h.ChainID)))
	b = append(b, h.ChainID...)
	b = append(b, h.Genesis[:]...)
	b = binary.BigEndian.AppendUint16(b, h.ListenPort)
	b = binary.BigEndian.AppendUint64(b, h.Nonce)
	return append(b, h.Services)
}

func decodeHello(b []byte) (hello, error) {
	var h hello
	if len(b) < 3 {
		return h, ErrHello
	}
	h.Version = binary.BigEndian.Uint16(b)
	n := int(b[2])
	b = b[3:]
	if len(b) != n+crypto.HashSize+2+8+1 {
		return h, ErrHello
	}
	h.ChainID = string(b[:n])
	copy(h.Genesis[:], b[n:])
	b = b[n+crypto.HashSize:]
	h.ListenPort = binary.BigEndian.Uint16(b)
	h.Nonce = binary.BigEndian.Uint64(b[2:])
	h.Services = b[10]
	return h, nil
}

// MaxAddrs : adresses échangées au plus par message.
const MaxAddrs = 100

func encodeAddrs(addrs []*net.TCPAddr) []byte {
	if len(addrs) > MaxAddrs {
		addrs = addrs[:MaxAddrs]
	}
	b := binary.BigEndian.AppendUint16(nil, uint16(len(addrs)))
	for _, a := range addrs {
		b = append(b, a.IP.To16()...)
		b = binary.BigEndian.AppendUint16(b, uint16(a.Port))
	}
	return b
}

func decodeAddrs(b []byte) ([]*net.TCPAddr, error) {
	if len(b) < 2 {
		return nil, ErrHello
	}
	n := int(binary.BigEndian.Uint16(b))
	b = b[2:]
	if n > MaxAddrs || len(b) != n*18 {
		return nil, ErrHello
	}
	out := make([]*net.TCPAddr, n)
	for i := range out {
		ip := make(net.IP, 16)
		copy(ip, b[:16])
		out[i] = &net.TCPAddr{IP: ip, Port: int(binary.BigEndian.Uint16(b[16:]))}
		b = b[18:]
	}
	return out, nil
}

// --- Messages de synchronisation ---
// Chaque requête porte un identifiant, repris par la réponse.

func encodeGetBlocks(req, from uint64, max uint16) []byte {
	b := binary.BigEndian.AppendUint64(nil, req)
	b = binary.BigEndian.AppendUint64(b, from)
	return binary.BigEndian.AppendUint16(b, max)
}

func decodeGetBlocks(b []byte) (req, from uint64, max uint16, err error) {
	if len(b) != 18 {
		return 0, 0, 0, ErrHello
	}
	return binary.BigEndian.Uint64(b), binary.BigEndian.Uint64(b[8:]), binary.BigEndian.Uint16(b[16:]), nil
}

func encodeTip(req, height uint64, hash crypto.Hash) []byte {
	b := binary.BigEndian.AppendUint64(nil, req)
	b = binary.BigEndian.AppendUint64(b, height)
	return append(b, hash[:]...)
}

func decodeTip(b []byte) (height uint64, hash crypto.Hash, err error) {
	if len(b) != 16+crypto.HashSize {
		return 0, hash, ErrHello
	}
	copy(hash[:], b[16:])
	return binary.BigEndian.Uint64(b[8:]), hash, nil
}

// encodeBlocks : identifiant, nombre, puis (longueur, bloc finalisé) pour chaque bloc.
func encodeBlocks(req uint64, items [][]byte) []byte {
	b := binary.BigEndian.AppendUint64(nil, req)
	b = binary.BigEndian.AppendUint16(b, uint16(len(items)))
	for _, it := range items {
		b = binary.BigEndian.AppendUint32(b, uint32(len(it)))
		b = append(b, it...)
	}
	return b
}

func decodeBlocks(b []byte) ([][]byte, error) {
	if len(b) < 10 {
		return nil, ErrHello
	}
	n := int(binary.BigEndian.Uint16(b[8:]))
	b = b[10:]
	items := make([][]byte, 0, min(n, 128))
	for i := 0; i < n; i++ {
		if len(b) < 4 {
			return nil, ErrHello
		}
		l := binary.BigEndian.Uint32(b)
		if uint64(l) > uint64(len(b)-4) {
			return nil, ErrHello
		}
		items = append(items, b[4:4+l])
		b = b[4+l:]
	}
	if len(b) != 0 {
		return nil, ErrHello
	}
	return items, nil
}

// requestID lit l'identifiant de requête en tête d'un message de synchronisation.
func requestID(b []byte) (uint64, bool) {
	if len(b) < 8 {
		return 0, false
	}
	return binary.BigEndian.Uint64(b), true
}
