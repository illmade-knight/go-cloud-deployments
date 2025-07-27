package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/illmade-knight/go-dataflow/pkg/types" // Added import for ConsumedMessage
	"github.com/illmade-knight/go-iot-dataflows/pkg/bigquery"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// TestPayload defines the structure of the data we expect to receive
// from Pub/Sub and insert into BigQuery. The `bigquery` tags are crucial
// for the BQ client to map struct fields to table columns.
type EnrichedTestPayload struct {
	DeviceID   string    `json:"device_id" bigquery:"device_id"`
	Timestamp  time.Time `json:"timestamp" bigquery:"timestamp"`
	Value      float64   `json:"value"     bigquery:"value"`
	ClientID   string    `json:"clientID"   bigquery:"clientID"`
	LocationID string    `json:"locationID" bigquery:"locationID"`
	Category   string    `json:"category"   bigquery:"category"`
}

// messageTransformer is the specific logic for this service that converts
// a raw byte payload from Pub/Sub into our target TestPayload struct.
// The signature has been updated to match the expected messagepipeline.MessageTransformer.
func messageTransformer(msg types.ConsumedMessage) (*EnrichedTestPayload, bool, error) {
	var payload EnrichedTestPayload
	// The transformation logic now operates on the Payload field of the consumed message.
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		log.Error().Err(err).Str("payload", string(msg.Payload)).Msg("Failed to unmarshal payload")
		return nil, false, fmt.Errorf("failed to unmarshal payload: %w", err)
	}
	// Return the transformed payload, a 'false' to indicate it should not be skipped,
	// and a nil error.
	return &payload, false, nil
}

func main() {
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()

	ctx := context.Background()

	// Load configuration using the flexible method that supports flags.
	cfg, err := bigquery.LoadConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load bigquery service config")
	}

	logger.Info().
		Str("project_id", cfg.ProjectID).
		Str("service_name", cfg.ServiceName).
		Str("dataflow_name", cfg.DataflowName).
		Str("director_url", cfg.ServiceDirectorURL).
		Str("subscription_id", cfg.Consumer.SubscriptionID).
		Msg("Preparing to start BigQuery service")

	// The BQServiceWrapper is generic, so we instantiate it with our specific
	// TestPayload type and provide our custom transformer function.
	bqService, err := bigquery.NewBQServiceWrapper[EnrichedTestPayload](
		ctx,
		cfg,
		logger,
		messageTransformer,
	)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to create BigQuery Service")
	}

	// Start the service (non-blocking).
	if err := bqService.Start(); err != nil {
		log.Fatal().Err(err).Msg("Failed to start BigQuery Service")
	}
	log.Info().Str("port", bqService.GetHTTPPort()).Msg("BigQuery Service is running")

	// Wait for a shutdown signal.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info().Msg("Shutdown signal received, stopping BigQuery Service...")

	// Gracefully shut down the service.
	bqService.Shutdown()
	log.Info().Msg("BigQuery Service stopped.")
}
