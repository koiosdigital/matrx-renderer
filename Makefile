.PHONY: build run test clean docker-build docker-run deps lint fmt

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=gofmt
GOLINT=golangci-lint

# Build parameters
BINARY_NAME=matrx-renderer
BINARY_PATH=./cmd/server
DOCKER_IMAGE=matrx-renderer

# Default target
all: deps lint test build

# Install dependencies
deps:
	$(GOMOD) download
	$(GOMOD) tidy

# Build the application
build:
	$(GOBUILD) -o $(BINARY_NAME) -v $(BINARY_PATH)

# Run the application
run:
	$(GOCMD) run $(BINARY_PATH)/main.go

# Run tests
test:
	$(GOTEST) -v ./...

# Run tests with coverage
test-coverage:
	$(GOTEST) -race -coverprofile=coverage.out -covermode=atomic ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html

# Clean build artifacts
clean:
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
	rm -f coverage.out coverage.html

# Format code
fmt:
	$(GOFMT) -s -w .

# Lint code
lint:
	$(GOLINT) run

# Build Docker image
docker-build:
	docker build -t $(DOCKER_IMAGE) .

# Run with Docker Compose
docker-run:
	docker-compose up --build

# Stop Docker Compose
docker-stop:
	docker-compose down

# Development setup
dev-setup: deps
	cp .env.example .env
	mkdir -p apps
	@echo "Development environment ready!"
	@echo "1. Start RabbitMQ: make docker-rabbitmq"
	@echo "2. Run the app: make run"

# Start only RabbitMQ for development
docker-rabbitmq:
	docker-compose up -d rabbitmq

# View logs
logs:
	docker-compose logs -f matrx-renderer

# Health check
health:
	curl -f http://localhost:8080/health || echo "Service not healthy"

# Security scan
security-scan:
	docker run --rm -v $(PWD):/app securecodewarrior/docker-security-scan:latest /app

# Performance test
perf-test:
	$(GOTEST) -bench=. -benchmem ./...
