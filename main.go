package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
)

type WebhookData struct {
	ID        string              `json:"id"`
	Timestamp time.Time           `json:"timestamp"`
	Headers   map[string][]string `json:"headers"`
	Body      json.RawMessage     `json:"body"`
	Method    string              `json:"method"`
	URL       string              `json:"url"`
}

type WebhookResponse struct {
	StatusCode int               `json:"statusCode"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	Delay      int               `json:"delay"` // Delay in milliseconds
}

type Server struct {
	redis *redis.Client
}

func NewServer() *Server {
	// Get Redis connection details from environment or use defaults
	redistHost := os.Getenv("REDIS_HOST")
	if redistHost == "" {
		redistHost = "localhost"
	}
	redistPort := os.Getenv("REDIS_PORT")
	if redistPort == "" {
		redistPort = "6379"
	}
	redisAddr := fmt.Sprintf("%s:%s", redistHost, redistPort)
	redisPassword := os.Getenv("REDIS_AUTH")
	redisDB := 0 // Default Redis DB

	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: redisPassword,
		DB:       redisDB,
	})

	return &Server{
		redis: rdb,
	}
}

func (s *Server) webhookHandler(w http.ResponseWriter, r *http.Request) {
	// Only accept POST requests
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading request body: %v", err)
		http.Error(w, "Error reading request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	// Generate a unique key for Redis
	key := fmt.Sprintf("webhook:%d", time.Now().UnixNano())

	// Create webhook data structure
	webhookData := WebhookData{
		ID:        key,
		Timestamp: time.Now(),
		Headers:   r.Header,
		Body:      json.RawMessage(body),
		Method:    r.Method,
		URL:       r.URL.String(),
	}

	// Convert to JSON
	jsonData, err := json.Marshal(webhookData)
	if err != nil {
		log.Printf("Error marshaling webhook data: %v", err)
		http.Error(w, "Error processing webhook data", http.StatusInternalServerError)
		return
	}

	// Save to Redis
	ctx := context.Background()
	err = s.redis.Set(ctx, key, jsonData, 24*time.Hour).Err() // TTL of 24 hours
	if err != nil {
		log.Printf("Error saving to Redis: %v", err)
		http.Error(w, "Error saving webhook data", http.StatusInternalServerError)
		return
	}

	// Also add to a list for easy retrieval
	err = s.redis.LPush(ctx, "webhooks:list", key).Err()
	if err != nil {
		log.Printf("Error adding to webhooks list: %v", err)
	}

	// Trim the list to keep only the last 1000 webhooks
	err = s.redis.LTrim(ctx, "webhooks:list", 0, 999).Err()
	if err != nil {
		log.Printf("Error trimming webhooks list: %v", err)
	}

	log.Printf("Webhook received and saved with key: %s", key)

	// Get configured response or use default
	responseConfig := s.getWebhookResponse(ctx)

	// Apply delay if configured
	if responseConfig.Delay > 0 {
		time.Sleep(time.Duration(responseConfig.Delay) * time.Millisecond)
	}

	// Set custom headers
	for headerKey, headerValue := range responseConfig.Headers {
		w.Header().Set(headerKey, headerValue)
	}

	// Set content type if not already set
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}

	// Set status code
	w.WriteHeader(responseConfig.StatusCode)

	// Write response body
	if responseConfig.Body != "" {
		w.Write([]byte(responseConfig.Body))
	} else {
		// Default response
		fmt.Fprintf(w, `{"status":"success","message":"Webhook received and saved","key":"%s"}`, key)
	}
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	// Check Redis connection
	ctx := context.Background()
	_, err := s.redis.Ping(ctx).Result()
	if err != nil {
		http.Error(w, "Redis connection failed", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"healthy","redis":"connected"}`)
}

func (s *Server) getWebhookResponse(ctx context.Context) WebhookResponse {
	// Get response config from Redis or return default
	data, err := s.redis.Get(ctx, "webhook:response:config").Result()
	if err != nil {
		// Return default response
		return WebhookResponse{
			StatusCode: 200,
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       "",
			Delay:      0,
		}
	}

	var response WebhookResponse
	if err := json.Unmarshal([]byte(data), &response); err != nil {
		// Return default on parse error
		return WebhookResponse{
			StatusCode: 200,
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       "",
			Delay:      0,
		}
	}

	return response
}

func (s *Server) apiWebhooksHandler(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	// Parse query parameters
	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			limit = parsedLimit
			if limit > 500 {
				limit = 500 // Cap at 500
			}
		}
	}

	search := strings.ToLower(r.URL.Query().Get("search"))
	sortBy := r.URL.Query().Get("sort")
	if sortBy == "" {
		sortBy = "timestamp-desc"
	}

	// Get the list of webhook keys
	keys, err := s.redis.LRange(ctx, "webhooks:list", 0, int64(limit*2)).Result() // Get more to allow for filtering
	if err != nil {
		log.Printf("Error getting webhooks list: %v", err)
		http.Error(w, "Error retrieving webhooks", http.StatusInternalServerError)
		return
	}

	var webhooks []WebhookData
	for _, key := range keys {
		data, err := s.redis.Get(ctx, key).Result()
		if err != nil {
			log.Printf("Error getting webhook data for key %s: %v", key, err)
			continue
		}

		var webhook WebhookData
		if err := json.Unmarshal([]byte(data), &webhook); err != nil {
			log.Printf("Error unmarshaling webhook data for key %s: %v", key, err)
			continue
		}

		// Apply search filter
		if search != "" {
			bodyStr := strings.ToLower(string(webhook.Body))
			headersStr := strings.ToLower(fmt.Sprintf("%v", webhook.Headers))
			urlStr := strings.ToLower(webhook.URL)
			methodStr := strings.ToLower(webhook.Method)

			if !strings.Contains(bodyStr, search) &&
				!strings.Contains(headersStr, search) &&
				!strings.Contains(urlStr, search) &&
				!strings.Contains(methodStr, search) {
				continue
			}
		}

		webhooks = append(webhooks, webhook)
		if len(webhooks) >= limit {
			break
		}
	}

	// Sort webhooks
	switch sortBy {
	case "timestamp-asc":
		sort.Slice(webhooks, func(i, j int) bool {
			return webhooks[i].Timestamp.Before(webhooks[j].Timestamp)
		})
	case "timestamp-desc":
		sort.Slice(webhooks, func(i, j int) bool {
			return webhooks[i].Timestamp.After(webhooks[j].Timestamp)
		})
	case "method":
		sort.Slice(webhooks, func(i, j int) bool {
			return webhooks[i].Method < webhooks[j].Method
		})
	case "url":
		sort.Slice(webhooks, func(i, j int) bool {
			return webhooks[i].URL < webhooks[j].URL
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"webhooks": webhooks,
		"count":    len(webhooks),
		"total":    len(keys),
	})
}

func (s *Server) listWebhooksHandler(w http.ResponseWriter, r *http.Request) {
	// Legacy endpoint - redirect to API
	s.apiWebhooksHandler(w, r)
}

func (s *Server) dashboardHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("templates/webhooks.html")
	if err != nil {
		log.Printf("Error parsing template: %v", err)
		http.Error(w, "Error loading dashboard", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if err := tmpl.Execute(w, nil); err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Error rendering dashboard", http.StatusInternalServerError)
	}
}

func (s *Server) getResponseConfigHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := context.Background()
	response := s.getWebhookResponse(ctx)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func (s *Server) setResponseConfigHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading request body: %v", err)
		http.Error(w, "Error reading request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var responseConfig WebhookResponse
	if err := json.Unmarshal(body, &responseConfig); err != nil {
		log.Printf("Error parsing response config: %v", err)
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	// Validate status code
	if responseConfig.StatusCode < 100 || responseConfig.StatusCode > 599 {
		responseConfig.StatusCode = 200
	}

	// Validate delay (max 30 seconds)
	if responseConfig.Delay < 0 || responseConfig.Delay > 30000 {
		responseConfig.Delay = 0
	}

	// Ensure headers map exists
	if responseConfig.Headers == nil {
		responseConfig.Headers = make(map[string]string)
	}

	// Save to Redis
	ctx := context.Background()
	configData, err := json.Marshal(responseConfig)
	if err != nil {
		log.Printf("Error marshaling response config: %v", err)
		http.Error(w, "Error saving configuration", http.StatusInternalServerError)
		return
	}

	err = s.redis.Set(ctx, "webhook:response:config", configData, 0).Err() // No TTL
	if err != nil {
		log.Printf("Error saving response config to Redis: %v", err)
		http.Error(w, "Error saving configuration", http.StatusInternalServerError)
		return
	}

	log.Printf("Webhook response configuration updated: Status=%d, Delay=%dms", responseConfig.StatusCode, responseConfig.Delay)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"success","message":"Response configuration updated"}`)
}

func (s *Server) clearAllWebhooksHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := context.Background()

	// Get all webhook keys from the list
	keys, err := s.redis.LRange(ctx, "webhooks:list", 0, -1).Result()
	if err != nil {
		log.Printf("Error getting webhooks list: %v", err)
		http.Error(w, "Error retrieving webhooks", http.StatusInternalServerError)
		return
	}

	// Delete all individual webhook data
	if len(keys) > 0 {
		err = s.redis.Del(ctx, keys...).Err()
		if err != nil {
			log.Printf("Error deleting webhook data: %v", err)
			http.Error(w, "Error clearing webhooks", http.StatusInternalServerError)
			return
		}
	}

	// Clear the webhooks list
	err = s.redis.Del(ctx, "webhooks:list").Err()
	if err != nil {
		log.Printf("Error clearing webhooks list: %v", err)
		http.Error(w, "Error clearing webhooks list", http.StatusInternalServerError)
		return
	}

	log.Printf("Cleared %d webhooks", len(keys))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"success","message":"Cleared %d webhooks","count":%d}`, len(keys), len(keys))
}

func main() {
	server := NewServer()

	// Test Redis connection
	ctx := context.Background()
	_, err := server.redis.Ping(ctx).Result()
	if err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	log.Println("Successfully connected to Redis")

	// Setup HTTP routes
	http.HandleFunc("/webhook", server.webhookHandler)
	http.HandleFunc("/health", server.healthHandler)
	http.HandleFunc("/webhooks", server.listWebhooksHandler)
	http.HandleFunc("/api/webhooks", server.apiWebhooksHandler)
	http.HandleFunc("/api/webhooks/clear", server.clearAllWebhooksHandler)
	http.HandleFunc("/api/response-config", server.getResponseConfigHandler)
	http.HandleFunc("/api/response-config/set", server.setResponseConfigHandler)
	http.HandleFunc("/", server.dashboardHandler)
	http.HandleFunc("/dashboard", server.dashboardHandler)

	// Get port from environment or use default
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Starting webhook server on port %s", port)
	log.Printf("Dashboard: http://localhost:%s/", port)
	log.Printf("Webhook endpoint: http://localhost:%s/webhook", port)
	log.Printf("Health check: http://localhost:%s/health", port)
	log.Printf("API endpoints: http://localhost:%s/api/webhooks", port)

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
