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
	MsgProposal  // bloc proposé, en attente de votes
	MsgFinalized // bloc finalisé + certificat
)

// IsGossip indique si un type de message est diffusé à tout le réseau.
func (t MsgType) IsGossip() bool { return t >= MsgTx && t <= MsgFinalized }

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
	if t < MsgHello || t > MsgFinalized {
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
}

func (h hello) encode() []byte {
	b := binary.BigEndian.AppendUint16(nil, h.Version)
	b = append(b, byte(len(h.ChainID)))
	b = append(b, h.ChainID...)
	b = append(b, h.Genesis[:]...)
	b = binary.BigEndian.AppendUint16(b, h.ListenPort)
	return binary.BigEndian.AppendUint64(b, h.Nonce)
}

func decodeHello(b []byte) (hello, error) {
	var h hello
	if len(b) < 3 {
		return h, ErrHello
	}
	h.Version = binary.BigEndian.Uint16(b)
	n := int(b[2])
	b = b[3:]
	if len(b) != n+crypto.HashSize+2+8 {
		return h, ErrHello
	}
	h.ChainID = string(b[:n])
	copy(h.Genesis[:], b[n:])
	b = b[n+crypto.HashSize:]
	h.ListenPort = binary.BigEndian.Uint16(b)
	h.Nonce = binary.BigEndian.Uint64(b[2:])
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
