package main

import (
	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"context"
	"errors"
	"fmt"
	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners" // Required for TCP listener
	"log/slog"
	"mochi/lib"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

// --- ADDED: Function to get secret from Secret Manager ---
func getSecret(ctx context.Context, secretName string) (string, error) {
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create secretmanager client: %w", err)
	}
	defer client.Close()

	req := &secretmanagerpb.AccessSecretVersionRequest{
		Name: secretName,
	}

	result, err := client.AccessSecretVersion(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to access secret version: %w", err)
	}

	return string(result.Payload.Data), nil
}

// --- END ADDED ---

func main() {
	// Setup structured logging.
	logLevel := os.Getenv("LOG_LEVEL")
	level := new(slog.LevelVar)
	switch logLevel {
	case "info":
		level.Set(slog.LevelInfo)
	default:
		level.Set(slog.LevelDebug)
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	ctx, stop := context.WithCancel(context.Background())

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// --- EDITED: Fetch Password from Secret Manager ---
	clientUser := os.Getenv("MQTT_USERNAME")
	secretName := os.Getenv("MQTT_PASS_SECRET_NAME") // We now get the secret name
	if clientUser == "" || secretName == "" {
		slog.Error("MQTT_USERNAME and MQTT_PASS_SECRET_NAME must be set")
		os.Exit(1)
	}

	clientPass, err := getSecret(ctx, secretName)
	if err != nil {
		slog.Error("Failed to fetch MQTT password from Secret Manager", "error", err)
		os.Exit(1)
	}
	// --- END EDITED ---

	server := mqtt.New(&mqtt.Options{
		Logger:       logger,
		InlineClient: true,
	})

	// --- EDITED: This block now uses the fetched credentials ---
	authRules := auth.AuthRules{
		{Username: auth.RString(clientUser), Password: auth.RString(clientPass), Allow: true},
	}
	if err := server.AddHook(new(auth.AllowHook), &auth.Options{
		Ledger: &auth.Ledger{Auth: authRules},
	}); err != nil {
		slog.Error("Failed to configure Mochi auth hook", "error", err)
		os.Exit(1)
	}
	// --- END EDITED ---

	tcpListener := listeners.NewTCP(listeners.Config{
		ID:      "tcp-listener",
		Address: ":1883",
	})
	if err := server.AddListener(tcpListener); err != nil {
		slog.Error("Failed to add MQTT TCP listener", "error", err)
		os.Exit(1)
	}

	go func() {
		slog.Info("Starting in-memory Mochi MQTT Broker with TCP listener on :1883")
		if err := server.Serve(); err != nil {
			slog.Error("Mochi server failed", "error", err)
		}
	}()

	// Pub/Sub passthrough section removed for brevity...

	httpPort := os.Getenv("PORT")
	if httpPort == "" {
		httpPort = "8080"
	}
	handler := lib.NewHandler(server)
	mux := http.NewServeMux()
	mux.HandleFunc("/load-test", handler.HandleLoadTest)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	httpServer := &http.Server{
		Addr:    ":" + httpPort,
		Handler: mux,
	}
	go func() {
		slog.Info("Starting HTTP trigger server", "port", httpPort)
		if err := httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server ListenAndServe failed", "error", err)
		}
	}()

	<-sigs
	slog.Info("Shutdown signal received, gracefully shutting down...")

	stop()
	// Shutdown servers...
}

// addAuthHook configures the server with basic authentication rules from env vars.
func addAuthHook(server *mqtt.Server) error {
	// These credentials will now be used by the external ingestion service.
	clientUser := os.Getenv("MQTT_USERNAME") // Using a generic name
	clientPass := os.Getenv("MQTT_PASSWORD") // Using a generic name

	if clientUser == "" || clientPass == "" {
		slog.Warn("MQTT_USERNAME or MQTT_PASSWORD not set. Auth hook will be permissive.")
		return server.AddHook(new(auth.AllowHook), nil)
	}

	authRules := auth.AuthRules{
		{Username: auth.RString(clientUser), Password: auth.RString(clientPass), Allow: true},
	}

	return server.AddHook(new(auth.AllowHook), &auth.Options{
		Ledger: &auth.Ledger{
			Auth: authRules,
		},
	})
}
