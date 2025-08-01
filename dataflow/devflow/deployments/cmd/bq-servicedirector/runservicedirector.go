package main

import (
	"context"
	_ "embed"
	"github.com/illmade-knight/go-cloud-manager/microservice/servicedirector"
	"github.com/illmade-knight/go-cloud-manager/pkg/servicemanager"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
	"os"
	"os/signal"
	"syscall"
	"time"
)

//go:embed services.yaml
var servicesYAML []byte

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

const (
	EnrichedTestPayloadSchema = "github.com/illmade-knight/go-dataflow-service/dataflow/devflow/EnrichedTestPayload"
)

func main() {
	logger := zerolog.New(os.Stdout).With().Timestamp().Str("component", "servicedirector").Logger()

	servicemanager.RegisterSchema(EnrichedTestPayloadSchema, EnrichedTestPayload{})
	// 1. Load Application Configuration
	cfg, err := servicedirector.NewConfig()
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to load servicedirector config")
	}
	ctx := context.Background()

	// 3. Load and Prepare the Architecture Definition
	arch := &servicemanager.MicroserviceArchitecture{}
	if err := yaml.Unmarshal(servicesYAML, arch); err != nil {
		log.Fatal().Err(err).Msg("Failed to parse embedded services.yaml")
	}
	// We require projectID to be present for deployment
	if cfg.ProjectID == "" {
		logger.Fatal().Msg("PROJECT_ID environment variable must be set")
	}
	// ensure we have a single ProjectID from here on - there should be no conflicts between cfg and arch
	arch.ProjectID = cfg.ProjectID

	cloudRunPort := os.Getenv("PORT")
	if cloudRunPort != "" {
		cfg.HTTPPort = cloudRunPort
	}
	// 4. Create the Main Director Service
	// This uses the production constructor
	director, err := servicedirector.NewServiceDirector(ctx, cfg, arch, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to create Director")
	}

	// 5. Start the Service and Wait for Shutdown
	if err := director.Start(); err != nil {
		logger.Fatal().Err(err).Msg("Failed to start Director")
	}
	logger.Info().Str("port", director.GetHTTPPort()).Msg("Director is running")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info().Msg("Shutdown signal received, stopping Director...")

	director.Shutdown(ctx)
	logger.Info().Msg("Director stopped.")
}
