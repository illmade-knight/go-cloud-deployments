package main

import (
	"context"
	_ "embed" // Required for go:embed
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/illmade-knight/go-dataflow-services/pkg/bigqueries"
	"github.com/illmade-knight/go-dataflow/pkg/messagepipeline"
	"github.com/rs/zerolog"
	"gopkg.in/yaml.v3"
)

//go:embed resources.yaml
var resourcesYAML []byte

// serviceConfig defines the minimal, local structs needed to unmarshal the
// service-specific resources.yaml file.
type serviceConfig struct {
	Subscriptions []struct {
		Name string `yaml:"name"`
	} `yaml:"subscriptions"`
	BigQueryDatasets []struct {
		Name string `yaml:"name"`
	} `yaml:"bigquery_datasets"`
	BigQueryTables []struct {
		Name string `yaml:"name"`
	} `yaml:"bigquery_tables"`
}

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
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()
	ctx := context.Background()

	// --- 1. Load Resource Configuration from Embedded YAML ---
	var resourceCfg serviceConfig
	if err := yaml.Unmarshal(resourcesYAML, &resourceCfg); err != nil {
		logger.Fatal().Err(err).Msg("Failed to parse embedded resources.yaml")
	}
	if len(resourceCfg.Subscriptions) != 1 {
		logger.Fatal().Msgf("Configuration error: expected exactly 1 subscription, found %d", len(resourceCfg.Subscriptions))
	}
	if len(resourceCfg.BigQueryDatasets) != 1 {
		logger.Fatal().Msgf("Configuration error: expected exactly 1 BigQuery dataset, found %d", len(resourceCfg.BigQueryDatasets))
	}
	if len(resourceCfg.BigQueryTables) != 1 {
		logger.Fatal().Msgf("Configuration error: expected exactly 1 BigQuery table, found %d", len(resourceCfg.BigQueryTables))
	}

	// --- 2. Load Runtime Configuration from Environment ---
	projectID := os.Getenv("PROJECT_ID")
	if projectID == "" {
		logger.Fatal().Msg("PROJECT_ID environment variable not set")
	}
	cfg := bigqueries.LoadConfigDefaults(projectID)

	dataflowName := os.Getenv("DATAFLOW_NAME")
	if dataflowName == "" {
		logger.Fatal().Msg("Dataflow name not specified")
	}
	cfg.DataflowName = dataflowName

	serviceDirectorURL := os.Getenv("SERVICE_DIRECTOR_URL")
	if serviceDirectorURL == "" {
		logger.Fatal().Msg("ServiceDirector URL not specified")
	}
	cfg.ServiceDirectorURL = serviceDirectorURL

	serviceName := os.Getenv("SERVICE_NAME")
	if serviceName == "" {
		logger.Fatal().Msg("Service name not specified")
	}
	cfg.ServiceName = serviceName

	if cloudRunPort := os.Getenv("PORT"); cloudRunPort != "" {
		cfg.HTTPPort = cloudRunPort
	}

	// --- 3. Set Resource Names from Embedded YAML ---
	cfg.InputSubscriptionID = resourceCfg.Subscriptions[0].Name
	cfg.BigQueryConfig.DatasetID = resourceCfg.BigQueryDatasets[0].Name
	cfg.BigQueryConfig.TableID = resourceCfg.BigQueryTables[0].Name

	logger.Info().
		Str("project_id", cfg.ProjectID).
		Str("subscription_id", cfg.InputSubscriptionID).
		Str("bigquery_dataset", cfg.BigQueryConfig.DatasetID).
		Str("bigquery_table", cfg.BigQueryConfig.TableID).
		Msg("Preparing to start BigQuery service")

	// --- 4. Service Initialization ---
	bqService, err := bigqueries.NewBQServiceWrapper[EnrichedPayload](ctx, cfg, logger, enrichedMessageTransformer)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to create BigQuery Service")
	}

	go func() {
		logger.Info().Str("port", bqService.GetHTTPPort()).Msg("BigQuery Service starting...")
		if err := bqService.Start(ctx); err != nil {
			logger.Error().Err(err).Msg("BigQuery Service failed during runtime")
		}
	}()

	// --- 5. Graceful Shutdown ---
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info().Msg("Shutdown signal received, stopping BigQuery Service...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := bqService.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("BigQuery Service shutdown failed")
	} else {
		logger.Info().Msg("BigQuery Service stopped.")
	}
}
