# Pulung Hooks - Webhook Receiver

A simple Go HTTP server that receives webhook POST requests and saves them to Redis.

## Features

- Receives HTTP POST requests on `/webhook` endpoint
- Saves webhook data (headers, body, timestamp, etc.) to Redis
- Provides health check endpoint at `/health`
- Lists recent webhooks at `/webhooks` endpoint
- Automatic cleanup (keeps last 1000 webhooks)
- TTL of 24 hours for webhook data

## Quick Start

### Using Docker Compose (Recommended)

1. Start the services:
```bash
docker-compose up -d
```

This will start both Redis and the webhook server.

### Manual Setup

1. Install and start Redis:
```bash
# On Ubuntu/Debian
sudo apt-get install redis-server
sudo systemctl start redis-server

# On macOS
brew install redis
brew services start redis
```

2. Run the Go application:
```bash
go run main.go
```

## Environment Variables

- `REDIS_ADDR`: Redis server address (default: `localhost:6379`)
- `REDIS_PASSWORD`: Redis password (optional)
- `PORT`: HTTP server port (default: `8080`)

## API Endpoints

### POST /webhook
Receives webhook data and saves it to Redis.

**Example:**
```bash
curl -X POST http://localhost:8080/webhook \
  -H "Content-Type: application/json" \
  -d '{"event": "user.created", "data": {"id": 123, "name": "John"}}'
```

**Response:**
```json
{
  "status": "success",
  "message": "Webhook received and saved",
  "key": "webhook:1634567890123456789"
}
```

### GET /health
Health check endpoint.

**Response:**
```json
{
  "status": "healthy",
  "redis": "connected"
}
```

### GET /webhooks
Lists the last 100 received webhooks.

**Response:**
```json
{
  "webhooks": [
    {
      "timestamp": "2023-10-22T10:30:00Z",
      "headers": {...},
      "body": {...},
      "method": "POST",
      "url": "/webhook"
    }
  ],
  "count": 1
}
```

## Testing

Test the webhook endpoint:

```bash
# Simple test
curl -X POST http://localhost:8080/webhook \
  -H "Content-Type: application/json" \
  -d '{"test": "data"}'

# Check health
curl http://localhost:8080/health

# List webhooks
curl http://localhost:8080/webhooks
```

## Redis Data Structure

Webhooks are stored in Redis with:
- Key: `webhook:{timestamp_nanoseconds}`
- Value: JSON object containing timestamp, headers, body, method, and URL
- List: `webhooks:list` contains webhook keys for easy retrieval
- TTL: 24 hours for individual webhook data

## Building

```bash
# Build binary
go build -o webhook-server

# Build Docker image
docker build -t pulung-hooks .
```