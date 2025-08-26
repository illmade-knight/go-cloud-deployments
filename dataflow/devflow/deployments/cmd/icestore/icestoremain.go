package main

import (
	"context"
	_ "embed" // Required for go:embed
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/illmade-knight/go-dataflow-services/pkg/icestore"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

//go:embed resources.yaml
var resourcesYAML []byte

// serviceConfig defines the minimal, local structs needed to unmarshal the
// service-specific resources.yaml file.
// REFACTOR: This struct now matches the structure of the provided resources.yaml.
type serviceConfig struct {
	Subscriptions []struct {
		Name string `yaml:"name"`
	} `yaml:"subscriptions"`
	GCSBuckets []struct {
		Name string `yaml:"name"`
	} `yaml:"gcs_buckets"`
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
		logger.Fatal().Msgf("Configuration error: expected exactly 1 subscription, found %d", len(resourceCfg.Subscriptions))
	}
	if len(resourceCfg.GCSBuckets) != 1 {
		logger.Fatal().Msgf("Configuration error: expected exactly 1 GCS bucket, found %d", len(resourceCfg.GCSBuckets))
	}

	// --- 2. Load Runtime Configuration from Environment ---
	projectID := os.Getenv("PROJECT_ID")
	if projectID == "" {
		projectID = os.Getenv("GOOGLE_CLOUD_PROJECT") // Fallback for compatibility
		if projectID == "" {
			logger.Fatal().Msg("PROJECT_ID or GOOGLE_CLOUD_PROJECT environment variable not set")
		}
	}
	cfg := icestore.LoadConfigDefaults(projectID)

	// Set resource names from the embedded YAML
	cfg.InputSubscriptionID = resourceCfg.Subscriptions[0].Name
	cfg.IceStore.BucketName = resourceCfg.GCSBuckets[0].Name

	// Override other defaults with environment variables
	if port := os.Getenv("PORT"); port != "" {
		cfg.HTTPPort = ":" + port
	}
	if bucketPrefix := os.Getenv("BUCKET_PREFIX"); bucketPrefix != "" {
		cfg.IceStore.ObjectPrefix = bucketPrefix
	}
	if cloudRunPort := os.Getenv("PORT"); cloudRunPort != "" {
		cfg.HTTPPort = cloudRunPort
	}

	logger.Info().
		Str("project_id", cfg.ProjectID).
		Str("subscription_id", cfg.InputSubscriptionID).
		Str("gcs_bucket", cfg.IceStore.BucketName).
		Msg("Preparing to start IceStore service")

	// --- 3. Service Initialization ---
	iceStoreService, err := icestore.NewIceStoreServiceWrapper(ctx, cfg, logger)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create IceStore Service")
	}

	// --- 4. Start Service and Handle Shutdown ---
	go func() {
		if err := iceStoreService.Start(ctx); err != nil {
			log.Fatal().Err(err).Msg("Failed to start IceStore Service")
		}
	}()
	log.Info().Str("port", iceStoreService.GetHTTPPort()).Msg("IceStore Service is running")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info().Msg("Shutdown signal received, stopping IceStore Service...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := iceStoreService.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("IceStore Service shutdown failed")
	} else {
		log.Info().Msg("IceStore Service stopped.")
	}
}
