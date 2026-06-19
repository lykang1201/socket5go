package protocol

import (
	"encoding/json"
	"io"
)

const (
	ConnTypeControl = "control"
	ConnTypeTunnel  = "tunnel"
)

const (
	MsgTypeRegister     = "register"
	MsgTypeRegisterAck  = "register_ack"
	MsgTypeNewConn      = "new_conn"
	MsgTypeNewConnAck   = "new_conn_ack"
	MsgTypeData         = "data"
	MsgTypeClose        = "close"
	MsgTypeHeartbeat    = "heartbeat"
	MsgTypeHeartbeatAck = "heartbeat_ack"
)

type Message struct {
	Type     string          `json:"type"`
	ClientID string          `json:"client_id,omitempty"`
	ConnID   string          `json:"conn_id,omitempty"`
	Data     []byte          `json:"data,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type RegisterRequest struct {
	ClientID string `json:"client_id"`
}

type RegisterResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

type NewConnRequest struct {
	ConnID string `json:"conn_id"`
	Port   int    `json:"port,omitempty"`
	Path   string `json:"path,omitempty"`
}

type NewConnResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

func ReadMessage(r io.Reader) (*Message, error) {
	var msg Message
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(&msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

func WriteMessage(w io.Writer, msg *Message) error {
	encoder := json.NewEncoder(w)
	return encoder.Encode(msg)
}
