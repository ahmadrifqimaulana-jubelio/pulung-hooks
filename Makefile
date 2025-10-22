.PHONY: build run test clean docker-up docker-down

# Build the application
build:
	go build -o webhook-server

# Run the application
run:
	go run main.go

# Run tests
test:
	./test.sh

# Clean build artifacts
clean:
	rm -f webhook-server

# Start with Docker Compose
docker-up:
	docker-compose up -d

# Stop Docker Compose
docker-down:
	docker-compose down

# View logs
logs:
	docker-compose logs -f

# Install dependencies
deps:
	go mod tidy
	go mod download

# Format code
fmt:
	go fmt ./...

# Check if Redis is running
redis-check:
	redis-cli ping || echo "Redis is not running"