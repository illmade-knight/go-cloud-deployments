// cmd/ingestion/main.go
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/illmade-knight/go-iot-dataflows/pkg/ingestion" // Import the application builder

	"github.com/rs/zerolog"
)

func main() {
	// Use a console writer for pretty, human-readable logs.
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()

	ctx := context.Background()

	// Load the configuration for the ingestion service.
	cfg, err := ingestion.LoadConfig()
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to load ingestion service config")
	}

	logger.Info().
		Str("project_id", cfg.ProjectID).
		Str("service_name", cfg.ServiceName).
		Str("producer_topic_id", cfg.Producer.TopicID).
		Str("mqtt_broker", cfg.MQTT.BrokerURL).
		Msg("Configuration loaded")

	// Create the IngestionService instance using the refactored wrapper.
	// The wrapper now internally handles message transformation, so we no longer
	// create or inject an 'extractor' here.
	ingestionService, err := ingestion.NewIngestionServiceWrapper(ctx, cfg, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to create IngestionService")
	}

	logger.Info().Msg("Ingestion service created successfully.")

	// Start the service (non-blocking).
	if err := ingestionService.Start(); err != nil {
		logger.Fatal().Err(err).Msg("Failed to start IngestionService")
	}
	logger.Info().Str("port", ingestionService.GetHTTPPort()).Msg("IngestionService is running")

	// Wait for a shutdown signal.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info().Msg("Shutdown signal received, stopping IngestionService...")

	// Gracefully shut down the service.
	ingestionService.Shutdown()
	logger.Info().Msg("IngestionService stopped.")
}
