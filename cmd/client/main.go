package main

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
	"github.com/robo-monk/tungo/pkg/protocol"
	"github.com/robo-monk/tungo/pkg/util"
)

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

	// Connect to WebSocket with more robust options
	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second

	// Connect to WebSocket
	log.Printf("Connecting to %s", wsURL.String())
	conn, _, err := dialer.DialContext(ctx, wsURL.String(), nil)
	if err != nil {
		return fmt.Errorf("error connecting to WebSocket: %w", err)
	}

	// Set read deadline to detect stale connections
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))

	// Setup ping handler to keep connection alive
	conn.SetPingHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	c.conn = conn

	log.Println("WebSocket connection established")
	return nil
}

// Start begins processing WebSocket messages
func (c *Client) Start(ctx context.Context) error {
	log.Println("Starting client")
	// Set up signal handling for graceful shutdown
	signal.Notify(c.interrupt, os.Interrupt, syscall.SIGTERM)

	// Start a goroutine to send periodic pings to keep connection alive
	pingTicker := time.NewTicker(20 * time.Second)
	defer pingTicker.Stop()

	go func() {
		for {
			select {
			case <-pingTicker.C:
				log.Println("Sending ping")
				if err := c.conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second)); err != nil {
					log.Printf("Error sending ping: %v", err)
				}
			case <-c.done:
				return
			}
		}
	}()

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
					log.Printf("Unexpected close error: %v", err)

					// Try to reconnect if this was an abnormal closure
					if websocket.IsCloseError(err, websocket.CloseAbnormalClosure) {
						log.Println("Attempting to reconnect...")
						if reconnectErr := c.reconnect(ctx); reconnectErr != nil {
							return fmt.Errorf("failed to reconnect: %w", reconnectErr)
						}
						continue
					}

					return fmt.Errorf("unexpected close error: %w", err)
				}
				log.Println("WebSocket connection closed")
				close(c.done)
				return nil
			}

			// Reset read deadline after successful read
			c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))

			// Process message asynchronously
			go c.handleMessage(data)
		}
	}
}

// reconnect attempts to re-establish the WebSocket connection
func (c *Client) reconnect(ctx context.Context) error {
	// First close the existing connection if it exists
	if c.conn != nil {
		c.conn.Close()
	}

	// Implement exponential backoff for reconnection
	backoff := 1 * time.Second
	maxBackoff := 30 * time.Second

	for i := 0; i < 5; i++ { // Try to reconnect up to 5 times
		log.Printf("Reconnection attempt %d in %v", i+1, backoff)

		select {
		case <-time.After(backoff):
			// Try to connect
			if err := c.Connect(ctx); err == nil {
				log.Println("Successfully reconnected")
				return nil
			} else {
				log.Printf("Reconnection failed: %v", err)
			}

			// Increase backoff for next attempt
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		case <-ctx.Done():
			return fmt.Errorf("context cancelled during reconnection")
		}
	}

	return fmt.Errorf("failed to reconnect after multiple attempts")
}

// handleMessage processes an incoming WebSocket message
func (c *Client) handleMessage(data []byte) {
	// Decode the message
	var request protocol.WebsocketRequestMessage
	if err := util.DecodeMessage(data, &request); err != nil {
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
	responseMsg := protocol.WebsocketResponseMessage{
		RequestID: request.ID,
		Timestamp: time.Now().UnixMilli(),
		Body:      body,
		Init: map[string]interface{}{
			"status":     response.StatusCode,
			"statusText": response.Status,
			"headers":    headers,
		},
	}

	// Send the response
	if err := util.SendMessage(c.conn, responseMsg); err != nil {
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
