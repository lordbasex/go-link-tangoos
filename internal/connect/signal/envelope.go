// Copiado de github.com/lordbasex/go-link-desk/backend-device/pkg/signalclient (MIT,
// mismo autor) para no arrastrar el módulo de go-link-desk; se mantiene igual al original.

// Copyright (c) 2026 Federico Pereira <lord.basex@gmail.com>

// Package signal is a generic client for the signalhub protocol v1.
// It knows nothing about TangoOS: it keeps one WebSocket open, reconnects
// with exponential backoff, and hands every message to a Handler.
//
// The protocol types are copied here on purpose: signalhub will live in
// its own repository and the protocol document is the only contract.
package signal

import "encoding/json"

// ProtocolVersion is sent in the URL as ?v=1.
const ProtocolVersion = "1"

// Message types of protocol v1.
const (
	TypeHello      = "hello"
	TypeRegister   = "register"
	TypeCode       = "code"
	TypeClaim      = "claim"
	TypePaired     = "paired"
	TypeReach      = "reach"
	TypeReached    = "reached"
	TypeRoomOpen   = "room_open"
	TypeRoomOpened = "room_opened"
	TypeRoomUpdate = "room_update"
	TypeRoomClose  = "room_close"
	// Invitations: the owner asks for one, signalhub answers with a long
	// invite (links, QR) and a 9 digit code.
	TypeInviteCreate  = "invite_create"
	TypeInviteCreated = "invite_created"
	TypeRoomsList     = "rooms_list"
	TypeRooms         = "rooms"
	TypeJoin          = "join"
	TypeJoined        = "joined"
	TypePeerJoined    = "peer_joined"
	TypeSignal        = "signal"
	TypePeerLeft      = "peer_left"
	TypeError         = "error"
)

// ICEServer follows the shape of RTCIceServer.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// RoomInfo is one entry of the public room directory.
type RoomInfo struct {
	RoomID string          `json:"room_id"`
	Meta   json.RawMessage `json:"meta,omitempty"`
}

// Envelope is the single JSON shape of every message.
type Envelope struct {
	Type     string `json:"type"`
	App      string `json:"app,omitempty"`
	DeviceID string `json:"device_id,omitempty"`
	// DeviceSecret binds DeviceID to this device on signalhub (register).
	DeviceSecret string          `json:"device_secret,omitempty"`
	Code         string          `json:"code,omitempty"`
	RoomID       string          `json:"room_id,omitempty"`
	Public       bool            `json:"public,omitempty"`
	InviteOnly   bool            `json:"invite_only,omitempty"`
	Invite       string          `json:"invite,omitempty"`
	Meta         json.RawMessage `json:"meta,omitempty"`
	Rooms        []RoomInfo      `json:"rooms,omitempty"`
	SessionID    string          `json:"session_id,omitempty"`
	PeerID       string          `json:"peer_id,omitempty"`
	Remote       string          `json:"remote,omitempty"`
	To           string          `json:"to,omitempty"`
	From         string          `json:"from,omitempty"`
	ICEServers   []ICEServer     `json:"ice_servers,omitempty"`
	Payload      json.RawMessage `json:"payload,omitempty"`
	Error        string          `json:"error,omitempty"`
}
