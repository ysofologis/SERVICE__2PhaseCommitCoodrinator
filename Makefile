.PHONY: dev build test clean run-coordinator run-participant

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
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
	$(GOCMD) run ./cmd/coordinator

# Run participant (development)
run-participant:
	$(GOCMD) run ./cmd/participant

# Tidy dependencies
tidy:
	$(GOCMD) mod tidy

# Format code
fmt:
	$(GOCMD) fmt ./...

# Vet code
vet:
	$(GOCMD) vet ./...
