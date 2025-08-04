package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/illmade-knight/go-dataflow-services/pkg/ingestion"
	"github.com/illmade-knight/go-dataflow/pkg/messagepipeline"
	"github.com/illmade-knight/go-dataflow/pkg/mqttconverter"
	"github.com/rs/zerolog"
)

var deviceFinder = regexp.MustCompile(`^[^/]+/([^/]+)/[^/]+$`)

func ingestionEnricher(_ context.Context, msg *messagepipeline.Message) (bool, error) {
	topic, ok := msg.Attributes["mqtt_topic"]
	if !ok {
		return false, nil
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
	return false, nil
}

func main() {
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()
	logger.Info().Msg("<<<<< Ingestion Service Main Starting >>>>>")

	// --- Configuration Loading ---
	projectID := os.Getenv("PROJECT_ID")
	if projectID == "" {
		logger.Fatal().Msg("PROJECT_ID environment variable not set")
	}
	cfg := ingestion.LoadConfigDefaults(projectID)

	serviceName := os.Getenv("SERVICE_NAME")
	if serviceName == "" {
		logger.Fatal().Msg("Service name not specified")
	}
	cfg.ServiceName = serviceName
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
	producerTopic := os.Getenv("INGESTION_BQ_TOPIC_ID")
	if producerTopic == "" {
		logger.Fatal().Msg("Producer Topic not specified")
	}
	cfg.OutputTopicID = producerTopic

	brokerURL := os.Getenv("MQTT_BROKER_URL")
	mqttTopic := os.Getenv("MQTT_TOPIC")
	mqttClientID := os.Getenv("MQTT_CLIENT_ID")
	mqttUsername := os.Getenv("MQTT_USERNAME")
	mqttPassword := os.Getenv("MQTT_PASSWORD")

	if brokerURL == "" || mqttTopic == "" || mqttClientID == "" || mqttUsername == "" || mqttPassword == "" {
		logger.Fatal().Str("URL", brokerURL).Str("topic", mqttTopic).Str("clientID", mqttClientID).Str("username", mqttUsername).Msg("mqtt not correctly configured")
	}
	cfg.MQTT = mqttconverter.MQTTClientConfig{
		BrokerURL:      brokerURL,
		Topic:          mqttTopic,
		ClientIDPrefix: mqttClientID,
		Username:       mqttUsername,
		Password:       mqttPassword,
		ConnectTimeout: 30 * time.Second,
	}
	cloudRunPort := os.Getenv("PORT")
	if cloudRunPort != "" {
		cfg.HTTPPort = cloudRunPort
	}

	// --- Service Initialization ---
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingestionService, err := ingestion.NewIngestionServiceWrapper(ctx, cfg, logger, ingestionEnricher)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to create IngestionService")
	}
	logger.Info().Msg("IngestionService wrapper created successfully.")

	// REFACTOR: This section replaces the previous startup logic to prevent deadlocks.
	// 1. Start non-blocking background components.
	// 2. Start the blocking HTTP server in a goroutine.
	// 3. Wait for an OS signal or a server error to trigger a graceful shutdown.

	// Start non-blocking background components first.
	err = ingestionService.Start(ctx)
	if err != nil {
		logger.Fatal().Err(err).Msg("IngestionService background components failed to start")
	}

	// Now, run the blocking HTTP server in a goroutine and wait for it to exit.
	errChan := make(chan error, 1)
	go func() {
		logger.Info().Str("port", ingestionService.GetHTTPPort()).Msg("Starting HTTP server...")
		// The blocking call is now in this goroutine. Its return value signals termination.
		errChan <- ingestionService.BaseServer.Start()
	}()

	// Wait for a shutdown signal.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	logger.Info().Msg("Service is running. Waiting for OS signal or server error to shut down.")

	select {
	case err = <-errChan:
		// The HTTP server terminated on its own.
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error().Err(err).Msg("HTTP server failed unexpectedly")
		} else {
			logger.Info().Msg("HTTP server shut down gracefully.")
		}
	case sig := <-quit:
		// An OS signal was received.
		logger.Info().Str("signal", sig.String()).Msg("OS signal received. Initiating shutdown.")
	}

	// --- Graceful Shutdown ---
	logger.Info().Msg("<<<<< Shutdown Initiated >>>>>")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	err = ingestionService.Shutdown(shutdownCtx)
	if err != nil {
		logger.Error().Err(err).Msg("IngestionService shutdown failed")
	} else {
		logger.Info().Msg("IngestionService stopped cleanly.")
	}
	logger.Info().Msg("<<<<< Ingestion Service Main Exiting >>>>>")
}
