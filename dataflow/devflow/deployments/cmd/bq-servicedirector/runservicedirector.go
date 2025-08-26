package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/illmade-knight/go-cloud-manager/microservice/servicedirector"
	"github.com/illmade-knight/go-cloud-manager/pkg/orchestration"
	"github.com/illmade-knight/go-cloud-manager/pkg/servicemanager"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

// The services.yaml embed contains the full architecture the Director will manage.
//
//go:embed services.yaml
var servicesYAML []byte

// The resources.yaml embed contains the specific Pub/Sub topics and subscriptions
// that the Director itself uses to receive commands and post completions.
//
//go:embed resources.yaml
var resourcesYAML []byte

// derivePubSubConfigFromResources parses the embedded resources.yaml to find the
// topic and subscription names the ServiceDirector needs for its own operation.
func derivePubSubConfigFromResources() (*servicedirector.PubsubConfig, error) {
	spec := &servicemanager.CloudResourcesSpec{}
	if err := yaml.Unmarshal(resourcesYAML, spec); err != nil {
		return nil, fmt.Errorf("failed to parse embedded resources.yaml: %w", err)
	}

	// Use the helper to create a simple lookup map from the resource spec.
	lookupMap := orchestration.ReadResourceMappings(spec)
	cfg := &servicedirector.PubsubConfig{}
	var ok bool

	// Get the command subscription name from the lookup map.
	cfg.CommandSubID, ok = lookupMap["command-subscription-id"]
	if !ok {
		return nil, fmt.Errorf("failed to find 'command-subscription-id' in resources.yaml lookup")
	}

	// Find the full subscription definition to get its associated topic name.
	var commandTopicName string
	for _, sub := range spec.Subscriptions {
		if sub.Name == cfg.CommandSubID {
			commandTopicName = sub.Topic
			break
		}
	}
	if commandTopicName == "" {
		return nil, fmt.Errorf("could not find topic for subscription '%s' in resources.yaml", cfg.CommandSubID)
	}
	cfg.CommandTopicID = commandTopicName

	// Get the completion topic name from the lookup map.
	cfg.CompletionTopicID, ok = lookupMap["completion-topic-id"]
	if !ok {
		return nil, fmt.Errorf("failed to find 'completion-topic-id' in resources.yaml lookup")
	}

	return cfg, nil
}

type EnrichedPayload struct {
	DeviceID   string    `bigquery:"device_id"`
	Timestamp  time.Time `bigquery:"timestamp"`
	Value      float64   `bigquery:"value"`
	ClientID   string    `bigquery:"client_id"`
	LocationID string    `bigquery:"location_id"`
	Category   string    `bigquery:"category"`
}

func main() {
	logger := zerolog.New(os.Stdout).With().Timestamp().Str("component", "servicedirector").Logger()
	ctx := context.Background()

	schemaIdentifier := "github.com/illmade-knight/go-dataflow-service/dataflow/devflow/EnrichedTestPayload"
	servicemanager.RegisterSchema(schemaIdentifier, EnrichedPayload{})

	// 1. Load base configuration from environment variables (e.g., Project ID).
	cfg, err := servicedirector.NewConfig()
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to load servicedirector config")
	}

	// 2. Load the microservice architecture that this Director will manage.
	arch := &servicemanager.MicroserviceArchitecture{}
	if err := yaml.Unmarshal(servicesYAML, arch); err != nil {
		log.Fatal().Err(err).Msg("Failed to parse embedded services.yaml")
	}

	// 3. Derive the Director's own Pub/Sub config from its embedded resources.yaml.
	pubsubCfg, err := derivePubSubConfigFromResources()
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to derive Pub/Sub config from embedded resources.yaml")
	}
	cfg.Commands = pubsubCfg // Inject the derived config.
	logger.Info().
		Str("command_topic", cfg.Commands.CommandTopicID).
		Str("completion_topic", cfg.Commands.CompletionTopicID).
		Str("command_subscription", cfg.Commands.CommandSubID).
		Msg("Successfully derived Pub/Sub configuration from resources.yaml.")

	// 4. Ensure consistent final configuration.
	if cfg.ProjectID == "" {
		logger.Fatal().Msg("PROJECT_ID environment variable must be set")
	}
	arch.ProjectID = cfg.ProjectID // Use project ID from env as the source of truth.

	if cloudRunPort := os.Getenv("PORT"); cloudRunPort != "" {
		cfg.HTTPPort = ":" + cloudRunPort
	}

	// 5. Create and start the Director service.
	director, err := servicedirector.NewServiceDirector(ctx, cfg, arch, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to create Director")
	}

	if err := director.Start(); err != nil {
		logger.Fatal().Err(err).Msg("Failed to start Director")
	}
	logger.Info().Str("port", director.GetHTTPPort()).Msg("Director is running")

	// 6. Wait for a shutdown signal to gracefully terminate.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info().Msg("Shutdown signal received, stopping Director...")

	director.Shutdown(ctx)
	logger.Info().Msg("Director stopped.")
}
