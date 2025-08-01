package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/illmade-knight/go-dataflow-services/pkg/icestore"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	logger := log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	ctx := context.Background()

	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		logger.Fatal().Msg("GOOGLE_CLOUD_PROJECT environment variable not set")
	}
	cfg := icestore.LoadConfigDefaults(projectID)

	// Override defaults with environment variables
	if port := os.Getenv("PORT"); port != "" {
		cfg.HTTPPort = ":" + port
	}
	if subID := os.Getenv("INPUT_SUBSCRIPTION_ID"); subID != "" {
		cfg.InputSubscriptionID = subID
	}
	if bucketName := os.Getenv("BUCKET_NAME"); bucketName != "" {
		cfg.IceStore.BucketName = bucketName
	}
	if bucketPrefix := os.Getenv("BUCKET_PREFIX"); bucketPrefix != "" {
		cfg.IceStore.ObjectPrefix = bucketPrefix
	}

	cloudRunPort := os.Getenv("PORT")
	if cloudRunPort != "" {
		cfg.HTTPPort = cloudRunPort
	}

	logger.Info().
		Str("project_id", cfg.ProjectID).
		Str("subscription_id", cfg.InputSubscriptionID).
		Str("gcs_bucket", cfg.IceStore.BucketName).
		Msg("Preparing to start IceStore service")

	iceStoreService, err := icestore.NewIceStoreServiceWrapper(ctx, cfg, logger)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create IceStore Service")
	}

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
