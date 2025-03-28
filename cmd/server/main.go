package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/robo-monk/tungo/pkg/protocol"
	"github.com/robo-monk/tungo/pkg/util"
)

// Server represents the WebSocket proxy server
type Server struct {
	port            int
	wsConn          *websocket.Conn
	wsConnMu        sync.RWMutex
	reqID           int64
	currentRequests map[int64]chan protocol.WebsocketResponseMessage
	requestsMu      sync.RWMutex
	upgrader        websocket.Upgrader
}

// NewServer creates a new proxy server instance
func NewServer(port int) *Server {
	return &Server{
		port:            port,
		currentRequests: make(map[int64]chan protocol.WebsocketResponseMessage),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true // In production, you might want to restrict this
			},
		},
	}
}

// Start starts the server
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// Status endpoint
	mux.HandleFunc("/_/", s.handleStatus)

	// WebSocket endpoint
	mux.HandleFunc("/_/_sock", s.handleWebSocket)

	// All other requests
	mux.HandleFunc("/", s.handleRequest)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: mux,
	}

	// Graceful shutdown
	go func() {
		<-ctx.Done()
		log.Println("Shutting down server...")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("Error during server shutdown: %v", err)
		}
	}()

	log.Printf("Server started on port: %d", s.port)
	return server.ListenAndServe()
}

// handleStatus returns the current server status
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.wsConnMu.RLock()
	clientConnected := s.wsConn != nil
	s.wsConnMu.RUnlock()

	status := struct {
		ReqID           int64 `json:"reqId"`
		ClientConnected bool  `json:"clientConnected"`
	}{
		ReqID:           atomic.LoadInt64(&s.reqID),
		ClientConnected: clientConnected,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// handleWebSocket handles WebSocket connections
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Error upgrading connection: %v", err)
		return
	}

	// Set ping handler to automatically respond with pongs
	conn.SetPingHandler(func(pingData string) error {
		log.Println("Received ping, responding with pong")
		return conn.WriteControl(websocket.PongMessage, []byte(pingData), time.Now().Add(5*time.Second))
	})

	// Set read deadline and update it on any message
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))

	// Set pong handler to update read deadline
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	s.wsConnMu.Lock()
	s.wsConn = conn
	s.wsConnMu.Unlock()

	log.Println("WebSocket opened")

	// Start a goroutine to send periodic pings to keep connection alive
	pingTicker := time.NewTicker(20 * time.Second)
	pingDone := make(chan struct{})

	go func() {
		defer pingTicker.Stop()
		for {
			select {
			case <-pingTicker.C:
				if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second)); err != nil {
					log.Printf("Error sending server ping: %v", err)
					return
				}
				log.Println("Server ping sent")
			case <-pingDone:
				return
			}
		}
	}()

	defer func() {
		close(pingDone)
		s.wsConnMu.Lock()
		s.wsConn = nil
		s.wsConnMu.Unlock()
		conn.Close()
		log.Println("WebSocket closed")
	}()

	// Handle incoming messages
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("Unexpected WebSocket error: %v", err)
			} else {
				log.Printf("WebSocket closed: %v", err)
			}
			break
		}

		// Update read deadline after successful read
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))

		log.Println("WebSocket message received")

		// Decode the message
		var response protocol.WebsocketResponseMessage
		if err := util.DecodeMessage(message, &response); err != nil {
			log.Printf("Error decoding message: %v", err)
			continue
		}

		// Forward the response to the pending request
		s.requestsMu.RLock()
		ch, exists := s.currentRequests[response.RequestID]
		s.requestsMu.RUnlock()

		if exists {
			ch <- response
		} else {
			log.Printf("No request found for response ID: %d", response.RequestID)
		}
	}
}

// handleRequest handles HTTP requests by forwarding them through WebSocket
func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	s.wsConnMu.RLock()
	wsConn := s.wsConn
	s.wsConnMu.RUnlock()

	if wsConn == nil {
		http.Error(w, "No WebSocket connection available", http.StatusServiceUnavailable)
		return
	}

	// Read request body
	body := make([]byte, 0)
	if r.Body != nil {
		if bodyBytes, err := io.ReadAll(r.Body); err == nil {
			body = bodyBytes
		}
	}

	// Create headers map
	headers := make(map[string]string)
	for name, values := range r.Header {
		if len(values) > 0 {
			headers[name] = values[0]
		}
	}

	// Generate request ID
	id := atomic.AddInt64(&s.reqID, 1) - 1

	// Create response channel
	respCh := make(chan protocol.WebsocketResponseMessage, 1)

	// Register the request
	s.requestsMu.Lock()
	s.currentRequests[id] = respCh
	s.requestsMu.Unlock()

	// Clean up when done
	defer func() {
		s.requestsMu.Lock()
		delete(s.currentRequests, id)
		s.requestsMu.Unlock()
		close(respCh)
	}()

	// Create the request message
	request := protocol.WebsocketRequestMessage{
		ID:        id,
		URL:       r.URL.String(),
		Timestamp: time.Now().UnixMilli(),
		Init: protocol.WebsocketRequestInit{
			Method:         r.Method,
			Headers:        headers,
			Body:           body,
			Mode:           "", // HTTP doesn't have this concept
			Credentials:    "", // HTTP doesn't have this concept
			Cache:          "", // HTTP doesn't have this concept
			Redirect:       "", // HTTP doesn't have this concept
			Referrer:       r.Referer(),
			ReferrerPolicy: "",    // HTTP doesn't have this concept
			Integrity:      "",    // HTTP doesn't have this concept
			Keepalive:      false, // HTTP doesn't have this concept
		},
	}

	// Send the request via WebSocket
	if err := util.SendMessage(wsConn, request); err != nil {
		http.Error(w, "Error sending request", http.StatusInternalServerError)
		log.Printf("Error sending request via WebSocket: %v", err)
		return
	}

	log.Printf("Request ID: %d, URL: %s", id, r.URL.String())

	// Wait for response with timeout
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	select {
	case resp := <-respCh:
		// Set response headers
		if headers, ok := resp.Init["headers"].(map[string]interface{}); ok {
			for key, value := range headers {
				if strValue, ok := value.(string); ok {
					w.Header().Set(key, strValue)
				}
			}
		}

		// Set status code
		if status, ok := resp.Init["status"].(float64); ok {
			w.WriteHeader(int(status))
		}

		// Write response body
		w.Write(resp.Body)
	case <-ctx.Done():
		http.Error(w, "Request timeout", http.StatusGatewayTimeout)
	}
}

func main() {
	port := 1821 // Default port

	// Allow port override from environment
	if portEnv := os.Getenv("PORT"); portEnv != "" {
		if p, err := strconv.Atoi(portEnv); err == nil {
			port = p
		}
	}

	// Create context that listens for termination signals
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signalCh
		log.Println("Received termination signal")
		cancel()
	}()

	// Create and start server
	server := NewServer(port)
	if err := server.Start(ctx); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
