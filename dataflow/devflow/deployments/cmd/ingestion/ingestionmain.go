package main

import (
	"context"
	"github.com/illmade-knight/go-dataflow/pkg/mqttconverter"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/illmade-knight/go-dataflow-services/pkg/ingestion"
	"github.com/illmade-knight/go-dataflow/pkg/messagepipeline"
	"github.com/rs/zerolog"
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
	// Use a console writer for pretty, human-readable logs.
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()

	ctx := context.Background()

	projectID := os.Getenv("PROJECT_ID")
	if projectID == "" {
		logger.Fatal().Msg("PROJECT_ID environment variable not set")
	}
	// Load the configuration for the ingestion service.
	cfg := ingestion.LoadConfigDefaults(projectID)

	serviceName := os.Getenv("SERVICE_NAME")
	dataflowName := os.Getenv("DATAFLOW_NAME")
	serviceDirectorURL := os.Getenv("SERVICE_DIRECTOR_URL")

	if serviceName == "" {
		logger.Fatal().Msg("Service name not specified")
	}
	cfg.ServiceName = serviceName
	if dataflowName == "" {
		logger.Fatal().Msg("Dataflow name not specified")
	}
	cfg.DataflowName = dataflowName
	if serviceDirectorURL == "" {
		logger.Fatal().Msg("ServiceDirector URL not specified")
	}
	cfg.ServiceDirectorURL = serviceDirectorURL

	// The difference here is the necessary MQTT info
	producerTopic := os.Getenv("INGESTION-BQ_TOPIC_ID")
	if producerTopic == "" {
		logger.Fatal().Msg("Producer Topic not specified")
	}
	cfg.OutputTopicID = producerTopic

	brokerURL := os.Getenv("MQTT_BROKER_URL")
	mqttTopic := os.Getenv("MQTT_TOPIC")
	mqttClientID := os.Getenv("MQTT_CLIENT_ID")
	mqttUsername := os.Getenv("MQTT_USERNAME")
	mqttPassword := os.Getenv("MQTT_PASSWORD")

	cfg.MQTT.ConnectTimeout = 30 * time.Second

	if brokerURL == "" || mqttTopic == "" || mqttClientID == "" || mqttUsername == "" || mqttPassword == "" {
		logger.Fatal().Str("URL", brokerURL).Str("topic", mqttTopic).Str("clientID", mqttClientID).Str("username", mqttUsername).Msg("mqtt not correctly configured")
	}

	cfg.MQTT = mqttconverter.MQTTClientConfig{
		BrokerURL:      brokerURL,
		Topic:          mqttTopic,
		ClientIDPrefix: mqttClientID,
		Username:       mqttUsername,
		Password:       mqttPassword,
	}

	cloudRunPort := os.Getenv("PORT")
	if cloudRunPort != "" {
		cfg.HTTPPort = cloudRunPort
	}

	logger.Info().
		Str("project_id", cfg.ProjectID).
		Str("service_name", cfg.ServiceName).
		Str("producer_topic_id", cfg.OutputTopicID).
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
