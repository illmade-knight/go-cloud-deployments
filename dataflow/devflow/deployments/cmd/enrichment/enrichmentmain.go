package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/illmade-knight/go-iot-dataflows/pkg/enrichment"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()

	// Load configuration using the flexible method that supports flags.
	cfg, err := enrichment.LoadConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load enrichment service config")
	}

	logger.Info().
		Str("project_id", cfg.ProjectID).
		Str("service_name", cfg.ServiceName).
		Str("dataflow_name", cfg.DataflowName).
		Str("director_url", cfg.ServiceDirectorURL).
		Str("subscription_id", cfg.Consumer.SubscriptionID).
		Msg("Preparing to start Enrichment Service")

	// The NewMessageEnricher function from the enrichment package perfectly matches
	// the MessageEnricherFactory signature required by the service wrapper.
	// We are instantiating the generic service to produce enrichment.EnrichedMessage.
	enrichmentService, err := enrichment.NewPublishMessageEnrichmentServiceWrapper(
		cfg,
		context.Background(),
		logger,
	)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create Enrichment Service")
	}

	// Start the service (non-blocking).
	if err := enrichmentService.Start(); err != nil {
		log.Fatal().Err(err).Msg("Failed to start Enrichment Service")
	}
	log.Info().Str("port", enrichmentService.GetHTTPPort()).Msg("Enrichment Service is running")

	// Wait for a shutdown signal.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info().Msg("Shutdown signal received, stopping Enrichment Service...")

	// Gracefully shut down the service.
	enrichmentService.Shutdown()
	log.Info().Msg("Enrichment Service stopped.")
}
