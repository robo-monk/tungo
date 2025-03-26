package client

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vmihailenco/msgpack/v5"
)

// WebsocketRequestMessage represents a request received via WebSocket
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

// WebsocketResponseMessage represents a response to be sent via WebSocket
type WebsocketResponseMessage struct {
	RequestID int64                  `msgpack:"requestId"`
	Timestamp int64                  `msgpack:"timestamp"`
	Body      []byte                 `msgpack:"body"`
	Init      map[string]interface{} `msgpack:"init"`
}

// Client represents the WebSocket client
type Client struct {
	serverURL   string
	forwardPort int
	conn        *websocket.Conn
	httpClient  *http.Client
	done        chan struct{}
	interrupt   chan os.Signal
}

// NewClient creates a new WebSocket client
func NewClient(serverURL string, forwardPort int) *Client {
	return &Client{
		serverURL:   serverURL,
		forwardPort: forwardPort,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		done:      make(chan struct{}),
		interrupt: make(chan os.Signal, 1),
	}
}

// Connect establishes a WebSocket connection
func (c *Client) Connect(ctx context.Context) error {
	// Parse the WebSocket URL
	u, err := url.Parse(c.serverURL)
	if err != nil {
		return fmt.Errorf("error parsing URL: %w", err)
	}

	// Create WebSocket URL
	wsURL := url.URL{
		Scheme: "ws",
		Host:   u.Host,
		Path:   "/_/_sock",
	}
	if u.Scheme == "https" {
		wsURL.Scheme = "wss"
	}

	// Connect to WebSocket
	log.Printf("Connecting to %s", wsURL.String())
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL.String(), nil)
	if err != nil {
		return fmt.Errorf("error connecting to WebSocket: %w", err)
	}
	c.conn = conn

	log.Println("WebSocket connection established")
	return nil
}

// Start begins processing WebSocket messages
func (c *Client) Start(ctx context.Context) error {
	// Set up signal handling for graceful shutdown
	signal.Notify(c.interrupt, os.Interrupt, syscall.SIGTERM)

	// Handle interrupts
	go func() {
		select {
		case <-c.interrupt:
			log.Println("Interrupt received, closing connection...")
			// Close WebSocket connection
			err := c.conn.WriteMessage(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			)
			if err != nil {
				log.Printf("Error during closing websocket: %v", err)
			}
			select {
			case <-c.done:
			case <-time.After(time.Second):
			}
			close(c.done)
		case <-ctx.Done():
			log.Println("Context cancelled, closing connection...")
			close(c.done)
		}
	}()

	// Process messages
	for {
		select {
		case <-c.done:
			return nil
		default:
			// Read message
			_, data, err := c.conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(
					err,
					websocket.CloseNormalClosure,
					websocket.CloseGoingAway,
				) {
					return fmt.Errorf("unexpected close error: %w", err)
				}
				log.Println("WebSocket connection closed")
				close(c.done)
				return nil
			}

			// Process message asynchronously
			go c.handleMessage(data)
		}
	}
}

// handleMessage processes an incoming WebSocket message
func (c *Client) handleMessage(data []byte) {
	// Decode the message
	var request WebsocketRequestMessage
	if err := msgpack.Unmarshal(data, &request); err != nil {
		log.Printf("Error decoding message: %v", err)
		return
	}

	log.Printf("Received request ID: %d, URL: %s", request.ID, request.URL)

	// Create a new URL for forwarding
	parsedURL, err := url.Parse(request.URL)
	if err != nil {
		log.Printf("Error parsing URL: %v", err)
		return
	}

	forwardURL := fmt.Sprintf("http://localhost:%d%s", c.forwardPort, parsedURL.Path)
	if parsedURL.RawQuery != "" {
		forwardURL += "?" + parsedURL.RawQuery
	}

	// Create a new request
	forwardReq, err := http.NewRequest(request.Init.Method, forwardURL, bytes.NewReader(request.Init.Body))
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return
	}

	// Set headers
	for key, value := range request.Init.Headers {
		forwardReq.Header.Set(key, value)
	}

	// Send the request
	response, err := c.httpClient.Do(forwardReq)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return
	}
	defer response.Body.Close()

	// Read response body
	body, err := io.ReadAll(response.Body)
	if err != nil {
		log.Printf("Error reading response body: %v", err)
		return
	}

	// Create headers map
	headers := make(map[string]string)
	for name, values := range response.Header {
		if len(values) > 0 {
			headers[name] = values[0]
		}
	}

	// Create response message
	responseMsg := WebsocketResponseMessage{
		RequestID: request.ID,
		Timestamp: time.Now().UnixMilli(),
		Body:      body,
		Init: map[string]interface{}{
			"status":     response.StatusCode,
			"statusText": response.Status,
			"headers":    headers,
		},
	}

	// Encode and send the response
	encoded, err := msgpack.Marshal(responseMsg)
	if err != nil {
		log.Printf("Error encoding response: %v", err)
		return
	}

	if err := c.conn.WriteMessage(websocket.BinaryMessage, encoded); err != nil {
		log.Printf("Error sending response: %v", err)
		return
	}

	log.Printf("Sent response for request ID: %d", request.ID)
}

func main() {
	// Define command-line flags
	serverURL := flag.String("server", "http://localhost:1821", "WebSocket server URL")
	forwardPort := flag.Int("forward", 1822, "Port to forward requests to")
	flag.Parse()

	// Allow overriding from environment variables
	if envServer := os.Getenv("SERVER_URL"); envServer != "" {
		*serverURL = envServer
	}
	if envPort := os.Getenv("FORWARD_PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil {
			*forwardPort = p
		}
	}

	// Create client
	client := NewClient(*serverURL, *forwardPort)

	// Create a cancelable context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Connect to the server
	if err := client.Connect(ctx); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	// Start processing messages
	if err := client.Start(ctx); err != nil {
		log.Fatalf("Error during operation: %v", err)
	}
}
