package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/illmade-knight/go-dataflow-services/pkg/bigqueries"
	"github.com/illmade-knight/go-dataflow/pkg/messagepipeline"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// EnrichedPayload defines the structure of the data we expect to receive
// from the enrichment service and insert into BigQuery.
type EnrichedPayload struct {
	DeviceID   string    `bigquery:"device_id"`
	Timestamp  time.Time `bigquery:"timestamp"`
	Value      float64   `bigquery:"value"`
	ClientID   string    `bigquery:"client_id"`
	LocationID string    `bigquery:"location_id"`
	Category   string    `bigquery:"category"`
}

// RawPayload defines the structure of the original, inner payload.
type RawPayload struct {
	DeviceID  string    `json:"device_id"`
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

// enrichedMessageTransformer is a pipeline-aware transformer. It unwraps the
// message from the enrichment service before transforming it for BigQuery.
func enrichedMessageTransformer(_ context.Context, msg *messagepipeline.Message) (*EnrichedPayload, bool, error) {
	var upstreamData messagepipeline.MessageData
	if err := json.Unmarshal(msg.Payload, &upstreamData); err != nil {
		return nil, false, fmt.Errorf("transformer: failed to unwrap upstream MessageData: %w", err)
	}

	var p RawPayload
	if err := json.Unmarshal(upstreamData.Payload, &p); err != nil {
		return nil, false, fmt.Errorf("transformer: failed to unmarshal inner raw payload: %w", err)
	}

	var locationID, category, clientID string
	if upstreamData.EnrichmentData != nil {
		locationID, _ = upstreamData.EnrichmentData["location"].(string)
		category, _ = upstreamData.EnrichmentData["serviceTag"].(string)
		clientID, _ = upstreamData.EnrichmentData["name"].(string)
	}

	return &EnrichedPayload{
		DeviceID:   p.DeviceID,
		Timestamp:  p.Timestamp,
		Value:      p.Value,
		ClientID:   clientID,
		LocationID: locationID,
		Category:   category,
	}, false, nil
}

func main() {
	logger := log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	ctx := context.Background()

	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		logger.Fatal().Msg("GOOGLE_CLOUD_PROJECT environment variable must be set")
	}

	cfg := bigqueries.LoadConfigDefaults(projectID)
	// Override defaults with env vars
	if port := os.Getenv("PORT"); port != "" {
		cfg.HTTPPort = ":" + port
	}
	if subID := os.Getenv("INPUT_SUBSCRIPTION_ID"); subID != "" {
		cfg.InputSubscriptionID = subID
	}
	if dataset := os.Getenv("BIGQUERY_DATASET"); dataset != "" {
		cfg.BigQueryConfig.DatasetID = dataset
	}
	if table := os.Getenv("BIGQUERY_TABLE"); table != "" {
		cfg.BigQueryConfig.TableID = table
	}

	logger.Info().
		Str("project_id", cfg.ProjectID).
		Str("subscription_id", cfg.InputSubscriptionID).
		Str("bigquery_dataset", cfg.BigQueryConfig.DatasetID).
		Str("bigquery_table", cfg.BigQueryConfig.TableID).
		Msg("Preparing to start BigQuery service")

	bqService, err := bigqueries.NewBQServiceWrapper[EnrichedPayload](ctx, cfg, logger, enrichedMessageTransformer)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to create BigQuery Service")
	}

	go func() {
		if err := bqService.Start(ctx); err != nil {
			logger.Fatal().Err(err).Msg("Failed to start BigQuery Service")
		}
	}()
	log.Info().Str("port", bqService.GetHTTPPort()).Msg("BigQuery Service is running")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info().Msg("Shutdown signal received, stopping BigQuery Service...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := bqService.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("BigQuery Service shutdown failed")
	} else {
		log.Info().Msg("BigQuery Service stopped.")
	}
}
