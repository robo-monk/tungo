package protocol

// WebsocketRequestMessage represents a request to be forwarded via WebSocket
type WebsocketRequestMessage struct {
	ID        int64                `msgpack:"id"`
	URL       string               `msgpack:"url"`
	Timestamp int64                `msgpack:"timestamp"`
	Init      WebsocketRequestInit `msgpack:"init"`
}

// WebsocketRequestInit contains request details
type WebsocketRequestInit struct {
	Method         string            `msgpack:"method"`
	Mode           string            `msgpack:"mode"`
	Headers        map[string]string `msgpack:"headers"`
	Body           []byte            `msgpack:"body"`
	Credentials    string            `msgpack:"credentials"`
	Cache          string            `msgpack:"cache"`
	Redirect       string            `msgpack:"redirect"`
	Referrer       string            `msgpack:"referrer"`
	ReferrerPolicy string            `msgpack:"referrerPolicy"`
	Integrity      string            `msgpack:"integrity"`
	Keepalive      bool              `msgpack:"keepalive"`
}

// WebsocketResponseMessage represents a response received via WebSocket
type WebsocketResponseMessage struct {
	RequestID int64                  `msgpack:"requestId"`
	Timestamp int64                  `msgpack:"timestamp"`
	Body      []byte                 `msgpack:"body"`
	Init      map[string]interface{} `msgpack:"init"`
}
