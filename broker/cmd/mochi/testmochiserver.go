package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
)

func main() {
	// Channel to listen for OS signals for graceful shutdown
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// Create and configure the MQTT Server
	server := mqtt.New(nil)
	addAuthHook(server)
	addMqttListener(server)

	// --- NEW: Health Check Server ---
	// Create a new HTTP server for health checks on a separate port
	httpServer := &http.Server{Addr: ":8081"}
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Run the HTTP server in a goroutine
	go func() {
		log.Println("Starting health check server on :8081")
		if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("HTTP server ListenAndServe: %v", err)
		}
	}()
	// --- END NEW ---

	// Run the MQTT server in a goroutine
	go func() {
		log.Println("Starting Mochi MQTT Broker")
		if err := server.Serve(); err != nil {
			log.Fatalf("MQTT server Serve: %v", err)
		}
	}()

	// Wait for shutdown signal
	<-sigs

	// --- NEW: Graceful Shutdown ---
	log.Println("Shutdown signal received, gracefully shutting down...")

	// Create a context for the HTTP server shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("HTTP server Shutdown error: %v", err)
	}

	server.Close()
	log.Println("Mochi MQTT Broker gracefully stopped.")
}

// Helper function to keep main clean
func addAuthHook(server *mqtt.Server) {
	clientUser := os.Getenv("CLIENT_USER")
	clientPass := os.Getenv("CLIENT_PASS")
	serviceUser := os.Getenv("SERVICE_USER")
	servicePass := os.Getenv("SERVICE_PASS")

	if clientUser == "" || clientPass == "" || serviceUser == "" || servicePass == "" {
		log.Fatal("FATAL: Missing one or more credential environment variables.")
	}

	// Create an auth ledger with rules for both users
	authRules := auth.AuthRules{
		{Username: auth.RString(clientUser), Password: auth.RString(clientPass), Allow: true},
		{Username: auth.RString(serviceUser), Password: auth.RString(servicePass), Allow: true},
	}

	if err := server.AddHook(new(auth.AllowHook), &auth.Options{Ledger: &auth.Ledger{Auth: authRules}}); err != nil {
		log.Fatalf("Failed to add auth hook: %v", err)
	}
}

// Helper function to keep main clean
func addMqttListener(server *mqtt.Server) {
	port := os.Getenv("PORT")
	if port == "" {
		port = "1883"
	}
	log.Printf("MQTT Broker will listen on port %s", port)

	tcp := listeners.NewTCP(listeners.Config{ID: "tcp-default", Address: ":" + port})
	if err := server.AddListener(tcp); err != nil {
		log.Fatalf("Failed to add MQTT listener: %v", err)
	}
}
