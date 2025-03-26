package util

import (
	"github.com/gorilla/websocket"
	"github.com/vmihailenco/msgpack/v5"
)

// EncodeMessage encodes a message using msgpack
func EncodeMessage(message interface{}) ([]byte, error) {
	return msgpack.Marshal(message)
}

// DecodeMessage decodes a message using msgpack
func DecodeMessage(data []byte, v interface{}) error {
	return msgpack.Unmarshal(data, v)
}

// SendMessage encodes and sends a message via WebSocket
func SendMessage(conn *websocket.Conn, message interface{}) error {
	data, err := EncodeMessage(message)
	if err != nil {
		return err
	}

	return conn.WriteMessage(websocket.BinaryMessage, data)
}
