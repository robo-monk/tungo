.PHONY: all build build-server build-client clean test

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get

# Binary names
SERVER_BINARY_NAME=server
CLIENT_BINARY_NAME=client

# Output directories
BIN_DIR=bin
SERVER_OUT=$(BIN_DIR)/$(SERVER_BINARY_NAME)
CLIENT_OUT=$(BIN_DIR)/$(CLIENT_BINARY_NAME)

# Main source files
SERVER_MAIN_GO=./cmd/server/main.go
CLIENT_MAIN_GO=./cmd/client/main.go

# Package location
SERVER_PACKAGE=./cmd/server
CLIENT_PACKAGE=./cmd/client
ALL_PACKAGES=./...

# Default target
all: clean build test

# Build both executables
build: build-server build-client

# Build server executable
build-server:
	@echo "Building server..."
	@mkdir -p $(BIN_DIR)
	$(GOBUILD) -o $(SERVER_OUT) $(SERVER_MAIN_GO)
	@echo "Server binary created at $(SERVER_OUT)"

# Build client executable
build-client:
	@echo "Building client..."
	@mkdir -p $(BIN_DIR)
	$(GOBUILD) -o $(CLIENT_OUT) $(CLIENT_MAIN_GO)
	@echo "Client binary created at $(CLIENT_OUT)"

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf $(BIN_DIR)
	$(GOCLEAN) $(ALL_PACKAGES)

# Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v $(ALL_PACKAGES)

# Run server
run-server: build-server
	@echo "Running server..."
	@$(SERVER_OUT)

# Run client
run-client: build-client
	@echo "Running client..."
	@$(CLIENT_OUT)

# Get dependencies
deps:
	@echo "Getting dependencies..."
	$(GOGET) -v $(ALL_PACKAGES)

# Install both executables
install: build
	@echo "Installing executables..."
	@mkdir -p $(GOPATH)/bin
	@cp $(SERVER_OUT) $(GOPATH)/bin/
	@cp $(CLIENT_OUT) $(GOPATH)/bin/
	@echo "Installed to $(GOPATH)/bin/"