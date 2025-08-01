package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/illmade-knight/go-dataflow-services/pkg/enrich"
	"github.com/illmade-knight/go-dataflow/pkg/cache"
	"github.com/illmade-knight/go-dataflow/pkg/messagepipeline"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// DeviceInfo is the data structure we want to enrich our messages with.
type DeviceInfo struct {
	ID         string
	Name       string
	ClientID   string
	LocationID string
	Category   string
}

// DeviceApplier applies the fetched DeviceInfo to the message's EnrichmentData map.
func DeviceApplier(msg *messagepipeline.Message, data DeviceInfo) {
	if msg.EnrichmentData == nil {
		msg.EnrichmentData = make(map[string]interface{})
	}
	msg.EnrichmentData["name"] = data.ClientID
	msg.EnrichmentData["location"] = data.LocationID
	msg.EnrichmentData["serviceTag"] = data.Category
}

func BasicKeyExtractor(msg *messagepipeline.Message) (string, bool) {
	if msg.EnrichmentData != nil {
		if deviceID, ok := msg.EnrichmentData["DeviceID"].(string); ok && deviceID != "" {
			return deviceID, true
		}
	}
	uid, ok := msg.Attributes["uid"]
	return uid, ok
}

func main() {
	logger := log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	ctx := context.Background()

	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		logger.Fatal().Msg("GOOGLE_CLOUD_PROJECT environment variable must be set")
	}

	cfg := enrich.LoadConfigDefaults(projectID)
	// Override defaults with env vars
	if port := os.Getenv("PORT"); port != "" {
		cfg.HTTPPort = ":" + port
	}
	if subID := os.Getenv("INPUT_SUBSCRIPTION_ID"); subID != "" {
		cfg.InputSubscriptionID = subID
	}
	if topicID := os.Getenv("OUTPUT_TOPIC_ID"); topicID != "" {
		cfg.OutputTopicID = topicID
	}
	if redisAddr := os.Getenv("REDIS_ADDR"); redisAddr != "" {
		cfg.CacheConfig.RedisConfig.Addr = redisAddr
	}
	if fsCollection := os.Getenv("FIRESTORE_COLLECTION"); fsCollection != "" {
		cfg.CacheConfig.FirestoreConfig.CollectionName = fsCollection
	}

	logger.Info().Str("project_id", cfg.ProjectID).Msg("Preparing to start Enrichment Service")

	// 1. Assemble the Fetcher using the Decorator Pattern
	fsClient, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to create Firestore client")
	}
	// The service will close the fetcher, which will close the redis client.
	// We are responsible for closing the firestore client.
	defer fsClient.Close()

	firestoreFetcher, err := cache.NewFirestore[string, DeviceInfo](ctx, cfg.CacheConfig.FirestoreConfig, fsClient, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to create Firestore fetcher")
	}

	redisFetcher, err := cache.NewRedisCache[string, DeviceInfo](ctx, &cfg.CacheConfig.RedisConfig, logger, firestoreFetcher)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to create Redis fetcher")
	}

	cloudRunPort := os.Getenv("PORT")
	if cloudRunPort != "" {
		cfg.HTTPPort = cloudRunPort
	}

	// 3. Create the service wrapper, injecting the fetcher and the enricher.
	enrichmentService, err := enrich.NewEnrichmentServiceWrapper[string, DeviceInfo](ctx, cfg, logger, redisFetcher, BasicKeyExtractor, DeviceApplier)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create Enrichment Service")
	}

	go func() {
		if err := enrichmentService.Start(ctx); err != nil {
			logger.Fatal().Err(err).Msg("Failed to start Enrichment Service")
		}
	}()
	log.Info().Str("port", enrichmentService.GetHTTPPort()).Msg("Enrichment Service is running")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info().Msg("Shutdown signal received, stopping Enrichment Service...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := enrichmentService.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("Enrichment Service shutdown failed")
	} else {
		log.Info().Msg("Enrichment Service stopped.")
	}
}
