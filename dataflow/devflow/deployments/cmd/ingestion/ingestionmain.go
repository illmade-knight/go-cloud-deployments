package main

import (
	"context"
	_ "embed" // Required for go:embed
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
	"gopkg.in/yaml.v3"
)

//go:embed resources.yaml
var resourcesYAML []byte

// serviceConfig defines the minimal, local structs needed to unmarshal the
// service-specific resources.yaml file.
type serviceConfig struct {
	Topics []struct {
		Name string `yaml:"name"`
	} `yaml:"topics"`
}

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

	// --- 1. Load Resource Configuration from Embedded YAML ---
	var resourceCfg serviceConfig
	if err := yaml.Unmarshal(resourcesYAML, &resourceCfg); err != nil {
		logger.Fatal().Err(err).Msg("Failed to parse embedded resources.yaml")
	}
	if len(resourceCfg.Topics) != 1 {
		logger.Fatal().Msgf("Configuration error: expected exactly 1 topic in resources.yaml, found %d", len(resourceCfg.Topics))
	}
	producerTopic := resourceCfg.Topics[0].Name

	// --- 2. Load Runtime Configuration from Environment ---
	projectID := os.Getenv("PROJECT_ID")
	if projectID == "" {
		logger.Fatal().Msg("PROJECT_ID environment variable not set")
	}
	cfg := ingestion.LoadConfigDefaults(projectID)
	cfg.OutputTopicID = producerTopic // Set from YAML

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
	if cloudRunPort := os.Getenv("PORT"); cloudRunPort != "" {
		cfg.HTTPPort = cloudRunPort
	}

	// --- 3. Service Initialization ---
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ingestionService, err := ingestion.NewIngestionServiceWrapper(ctx, cfg, logger, ingestionEnricher)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to create IngestionService")
	}
	logger.Info().Msg("IngestionService wrapper created successfully.")

	// --- 4. Start Service and Handle Shutdown ---
	err = ingestionService.Start(ctx)
	if err != nil {
		logger.Fatal().Err(err).Msg("IngestionService background components failed to start")
	}

	errChan := make(chan error, 1)
	go func() {
		logger.Info().Str("port", ingestionService.GetHTTPPort()).Msg("Starting HTTP server...")
		errChan <- ingestionService.BaseServer.Start()
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	logger.Info().Msg("Service is running. Waiting for OS signal or server error to shut down.")

	select {
	case err = <-errChan:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error().Err(err).Msg("HTTP server failed unexpectedly")
		} else {
			logger.Info().Msg("HTTP server shut down gracefully.")
		}
	case sig := <-quit:
		logger.Info().Str("signal", sig.String()).Msg("OS signal received. Initiating shutdown.")
	}

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
