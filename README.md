# Tungo: Secure and Reliable WebSocket Proxy

Tungo is a powerful WebSocket proxy written in Go. Fast and lightweight alternative to LocalTunnel.

### Installation

Clone the repository and build the binaries:

#### Server
```bash
git clone https://github.com/yourusername/tungo.git
cd tungo
make build-server
./bin/server
# expose "127.0.0.1:1821" to the internet
```

#### Client
```bash
git clone https://github.com/yourusername/tungo.git
cd tungo
make build-client
./bin/client -server https://yourserver.com -forward 3000
```

### Usage
Expose your local server running on port 3000:

```bash
./bin/client -server ws://yourserver.com -forward 3000
```

## Why Tungo?
- Tungo keeps the request intact, so forwarding Stripe webhooks to your local server is possible (unlike LocalTunnel)
- Lightweight and compiled to native code
- Tungo provides automatic reconnection and more robust WebSocket handling.

##### public tungo server coming soon