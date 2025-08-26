package main

import (
	"context"
	_ "embed"
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
	"gopkg.in/yaml.v3"
)

//go:embed resources.yaml
var resourcesYAML []byte

// serviceConfig defines the minimal, local structs for unmarshaling resources.yaml.
type serviceConfig struct {
	Topics []struct {
		Name string `yaml:"name"`
	} `yaml:"topics"`
	Subscriptions []struct {
		Name string `yaml:"name"`
	} `yaml:"subscriptions"`
	FirestoreDatabases []struct {
		Name string `yaml:"name"`
	} `yaml:"firestore_databases"`
	FirestoreCollections []struct {
		Name     string `yaml:"name"`
		Database string `yaml:"database"`
	} `yaml:"firestore_databases"`
}

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

	// --- 1. Load Resource Configuration from Embedded YAML ---
	var resourceCfg serviceConfig
	if err := yaml.Unmarshal(resourcesYAML, &resourceCfg); err != nil {
		logger.Fatal().Err(err).Msg("Failed to parse embedded resources.yaml")
	}
	if len(resourceCfg.Subscriptions) != 1 {
		logger.Fatal().Msgf("Config error: expected 1 subscription, found %d", len(resourceCfg.Subscriptions))
	}
	if len(resourceCfg.Topics) != 1 {
		logger.Fatal().Msgf("Config error: expected 1 topic, found %d", len(resourceCfg.Topics))
	}
	if len(resourceCfg.FirestoreDatabases) != 1 {
		logger.Fatal().Msgf("Config error: expected 1 firestore database, found %d", len(resourceCfg.FirestoreDatabases))
	}
	if len(resourceCfg.FirestoreCollections) != 1 {
		logger.Fatal().Msgf("Config error: expected 1 firestore collections, found %d", len(resourceCfg.FirestoreDatabases))
	}

	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		logger.Fatal().Msg("GOOGLE_CLOUD_PROJECT environment variable must be set")
	}

	cfg := enrich.LoadConfigDefaults(projectID)
	// Override defaults with env vars
	if port := os.Getenv("PORT"); port != "" {
		cfg.HTTPPort = ":" + port
	}
	cfg.InputSubscriptionID = resourceCfg.Subscriptions[0].Name
	cfg.OutputTopicID = resourceCfg.Topics[0].Name
	// Note: The Firestore client only needs the projectID, but this confirms the link.
	_ = resourceCfg.FirestoreDatabases[0].Name
	cfg.CacheConfig.FirestoreConfig.CollectionName = resourceCfg.FirestoreCollections[0].Name

	// Override Redis defaults with env vars
	//if redisAddr := os.Getenv("REDIS_ADDR"); redisAddr != "" {
	//	cfg.CacheConfig.RedisConfig.Addr = redisAddr
	//}

	logger.Info().Str("project_id", cfg.ProjectID).Msg("Preparing to start Enrichment Service")

	// 1. Assemble the Fetcher using the Decorator Pattern
	fsClient, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to create Firestore client")
	}
	// The service will close the fetcher, which will close the redis client.
	// We are responsible for closing the firestore client.
	defer func() {
		_ = fsClient.Close()
	}()

	firestoreFetcher, err := cache.NewFirestore[string, DeviceInfo](ctx, cfg.CacheConfig.FirestoreConfig, fsClient, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to create Firestore fetcher")
	}

	//redisFetcher, err := cache.NewRedisCache[string, DeviceInfo](ctx, &cfg.CacheConfig.RedisConfig, logger, firestoreFetcher)
	//if err != nil {
	//	logger.Fatal().Err(err).Msg("Failed to create Redis fetcher")
	//}

	cloudRunPort := os.Getenv("PORT")
	if cloudRunPort != "" {
		cfg.HTTPPort = cloudRunPort
	}

	// 3. Create the service wrapper, injecting the fetcher and the enricher.
	enrichmentService, err := enrich.NewEnrichmentServiceWrapper[string, DeviceInfo](ctx, cfg, logger, firestoreFetcher, BasicKeyExtractor, DeviceApplier)
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
