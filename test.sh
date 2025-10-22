#!/bin/bash

# Test script for webhook server

echo "Testing Webhook Server..."

# Check if server is running
if ! curl -s http://localhost:8080/health > /dev/null; then
    echo "❌ Server is not running. Please start it first with: go run main.go"
    exit 1
fi

echo "✅ Server is running"

# Test health endpoint
echo "Testing health endpoint..."
HEALTH=$(curl -s http://localhost:8080/health)
echo "Health response: $HEALTH"

# Test webhook endpoint
echo "Testing webhook endpoint..."
RESPONSE=$(curl -s -X POST http://localhost:8080/webhook \
    -H "Content-Type: application/json" \
    -d '{"event": "test", "timestamp": "'$(date -Iseconds)'", "data": {"message": "Hello from test script"}}')

echo "Webhook response: $RESPONSE"

# Wait a moment
sleep 1

# Test list webhooks
echo "Testing list webhooks endpoint..."
WEBHOOKS=$(curl -s http://localhost:8080/webhooks)
echo "Webhooks response: $WEBHOOKS"

echo "✅ All tests completed!"