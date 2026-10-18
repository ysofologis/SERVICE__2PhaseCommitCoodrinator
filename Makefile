.PHONY: dev build test clean run-coordinator run-participant run

# Go parameters
GOCMD=go
# The project uses modernc.org/sqlite (pure Go), so we build with CGO
# disabled. This avoids requiring a working system C toolchain. If your
# environment has working cgo and you want to use it, override CGO_ENABLED=1.
CGO_ENABLED ?= 0
export CGO_ENABLED
GOBUILD=CGO_ENABLED=$(CGO_ENABLED) $(GOCMD) build
GOTEST=CGO_ENABLED=$(CGO_ENABLED) $(GOCMD) test
GOCLEAN=$(GOCMD) clean
GOGET=$(GOCMD) get
BINARY_NAME=coordinator
PARTICIPANT_BINARY=participant

# Build the project
build:
	$(GOBUILD) -o $(BINARY_NAME) ./cmd/coordinator
	$(GOBUILD) -o $(PARTICIPANT_BINARY) ./cmd/participant

# Run tests
test:
	$(GOTEST) -v ./...

# Clean build artifacts
clean:
	$(GOCLEAN)
	rm -f $(BINARY_NAME) $(PARTICIPANT_BINARY)

# Run coordinator (development)
run-coordinator:
	CGO_ENABLED=$(CGO_ENABLED) $(GOCMD) run ./cmd/coordinator

# Run participant (development)
run-participant:
	CGO_ENABLED=$(CGO_ENABLED) $(GOCMD) run ./cmd/participant

# Bring up the full local stack: 1 coordinator + 2 participants, in
# background. Stops cleanly on Ctrl-C.
run:
	@echo "Starting 2 participants + 1 coordinator..."
	@trap 'kill 0' INT; \
	 CGO_ENABLED=$(CGO_ENABLED) $(GOCMD) run ./cmd/participant -port 8081 & \
	 CGO_ENABLED=$(CGO_ENABLED) $(GOCMD) run ./cmd/participant -port 8082 & \
	 CGO_ENABLED=$(CGO_ENABLED) $(GOCMD) run ./cmd/coordinator & \
	 wait

# Tidy dependencies
tidy:
	$(GOCMD) mod tidy

# Format code
fmt:
	$(GOCMD) fmt ./...

# Vet code
vet:
	$(GOCMD) vet ./...
