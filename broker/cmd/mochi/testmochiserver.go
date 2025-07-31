package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
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

	// ... (logging setup remains the same) ...
	logLevel := os.Getenv("LOG_LEVEL")
	level := new(slog.LevelVar)
	server.Log = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))
	switch logLevel {
	case "info":
		level.Set(slog.LevelInfo)
	default:
		level.Set(slog.LevelDebug)
	}

	addAuthHook(server)
	// MODIFIED: Pass a dedicated port for MQTT
	addMqttListener(server, "1883")

	// --- Health Check Server ---
	// MODIFIED: Use the $PORT environment variable for the health check server
	httpPort := os.Getenv("PORT")
	if httpPort == "" {
		httpPort = "8080" // A common default for local development
	}
	httpServer := &http.Server{Addr: ":" + httpPort}
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// Run the HTTP server in a goroutine
	go func() {
		log.Printf("Starting health check server on port %s", httpPort)
		if err := httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server ListenAndServe: %v", err)
		}
	}()

	// Run the MQTT server in a goroutine
	go func() {
		log.Println("Starting Mochi MQTT Broker")
		if err := server.Serve(); err != nil {
			log.Fatalf("MQTT server Serve: %v", err)
		}
	}()

	// Wait for shutdown signal - THIS IS THE LINE THAT KEEPS THE SERVER UP
	<-sigs

	// --- Graceful Shutdown ---
	log.Println("Shutdown signal received, gracefully shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("HTTP server Shutdown error: %v", err)
	}

	_ = server.Close()
	log.Println("Mochi MQTT Broker gracefully stopped.")
}

// ... addAuthHook remains the same ...
func addAuthHook(server *mqtt.Server) {
	clientUser := os.Getenv("CLIENT_USER")
	clientPass := os.Getenv("CLIENT_PASS")
	serviceUser := os.Getenv("SERVICE_USER")
	servicePass := os.Getenv("SERVICE_PASS")

	if clientUser == "" || clientPass == "" || serviceUser == "" || servicePass == "" {
		log.Fatal("FATAL: Missing one or more credential environment variables.")
	}

	authRules := auth.AuthRules{
		{Username: auth.RString(clientUser), Password: auth.RString(clientPass), Allow: true},
		{Username: auth.RString(serviceUser), Password: auth.RString(servicePass), Allow: true},
	}

	if err := server.AddHook(new(auth.AllowHook), &auth.Options{Ledger: &auth.Ledger{Auth: authRules}}); err != nil {
		log.Fatalf("Failed to add auth hook: %v", err)
	}
}

// MODIFIED: Takes a port argument and no longer uses the $PORT env var
func addMqttListener(server *mqtt.Server, port string) {
	log.Printf("MQTT Broker will listen on port %s", port)

	tcp := listeners.NewTCP(listeners.Config{ID: "tcp-default", Address: ":" + port})
	if err := server.AddListener(tcp); err != nil {
		log.Fatalf("Failed to add MQTT listener: %v", err)
	}
}
