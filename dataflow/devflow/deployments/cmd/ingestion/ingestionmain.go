package main

import (
	"context"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/illmade-knight/go-dataflow-services/pkg/ingestion"
	"github.com/illmade-knight/go-dataflow/pkg/messagepipeline"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// deviceFinder is a regex to extract a device ID from a standard MQTT topic structure.
var deviceFinder = regexp.MustCompile(`^[^/]+/([^/]+)/[^/]+$`)

// ingestionEnricher is a message enricher that adds basic metadata from the MQTT topic.
// This is the core "transformation" logic for the ingestion service.
func ingestionEnricher(_ context.Context, msg *messagepipeline.Message) (bool, error) {
	topic, ok := msg.Attributes["mqtt_topic"]
	if !ok {
		return false, nil // No topic, nothing to do.
	}

	var deviceID string
	if matches := deviceFinder.FindStringSubmatch(topic); len(matches) > 1 {
		deviceID = matches[1]
	}

	if msg.EnrichmentData == nil {
		msg.EnrichmentData = make(map[string]interface{})
	}
	msg.EnrichmentData["DeviceID"] = deviceID
	msg.EnrichmentData["Topic"] = topic
	msg.EnrichmentData["Timestamp"] = msg.PublishTime

	return false, nil // Do not skip, no error.
}

func main() {
	logger := log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	ctx := context.Background()

	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		logger.Fatal().Msg("GOOGLE_CLOUD_PROJECT environment variable must be set")
	}

	cfg := ingestion.LoadConfigDefaults(projectID)
	// Override defaults with environment variables
	if port := os.Getenv("PORT"); port != "" {
		cfg.HTTPPort = ":" + port
	}
	if topic := os.Getenv("OUTPUT_TOPIC_ID"); topic != "" {
		cfg.OutputTopicID = topic
	}
	// MQTT config is loaded from env by default in LoadConfigDefaults

	logger.Info().
		Str("project_id", cfg.ProjectID).
		Str("output_topic", cfg.OutputTopicID).
		Str("mqtt_broker", cfg.MQTT.BrokerURL).
		Msg("Preparing to start Ingestion service")

	ingestionService, err := ingestion.NewIngestionServiceWrapper(ctx, cfg, logger, ingestionEnricher)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to create IngestionService")
	}

	go func() {
		if err := ingestionService.Start(ctx); err != nil {
			logger.Fatal().Err(err).Msg("Failed to start IngestionService")
		}
	}()
	logger.Info().Str("port", ingestionService.GetHTTPPort()).Msg("IngestionService is running")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info().Msg("Shutdown signal received, stopping IngestionService...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := ingestionService.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("IngestionService shutdown failed")
	} else {
		logger.Info().Msg("IngestionService stopped.")
	}
}
