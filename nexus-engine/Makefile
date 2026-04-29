.PHONY: proto build run test clean

BINARY := nexus-engine
PROTO_DIR := contracts/nexus/nodeagent/v1
GEN_DIR := gen/nodeagent/v1

# Generate protobuf Go stubs
proto:
	@echo "→ generating Go protobuf stubs"
	mkdir -p $(GEN_DIR)
	buf generate --template buf.gen.yaml $(PROTO_DIR)

# Build binary
build:
	@echo "→ building $(BINARY)"
	go build -o $(BINARY) ./cmd/

# Run in dev mode
run:
	NEXUS_MODE=dev go run ./cmd/

# Run tests
test:
	go test ./... -v -count=1 -race

# Run tests with coverage
test-coverage:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html

# Format and vet
lint:
	go vet ./...
	@command -v golangci-lint &>/dev/null && golangci-lint run || echo "golangci-lint not installed"

# Install proto tools
proto-tools:
	go install github.com/bufbuild/buf/cmd/buf@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

clean:
	rm -f $(BINARY) coverage.out coverage.html
	rm -rf $(GEN_DIR)/*.go
