//go:build integration

package e2e

import (
	"cloud.google.com/go/firestore"
	"cloud.google.com/go/pubsub"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/illmade-knight/go-cloud-manager/microservice"
	"github.com/illmade-knight/go-cloud-manager/microservice/servicedirector"
	"github.com/illmade-knight/go-cloud-manager/pkg/servicemanager"

	"github.com/illmade-knight/go-dataflow/pkg/cache"
	"github.com/illmade-knight/go-dataflow/pkg/messagepipeline"
	"github.com/illmade-knight/go-dataflow/pkg/types"
	"github.com/illmade-knight/go-iot-dataflows/pkg/bigquery"
	"github.com/illmade-knight/go-iot-dataflows/pkg/enrichment"
	"github.com/illmade-knight/go-iot-dataflows/pkg/icestore"
	"github.com/illmade-knight/go-iot-dataflows/pkg/ingestion"
	"github.com/illmade-knight/go-test/loadgen"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"
)

// setupCommandInfrastructure creates topics and subscriptions needed for command-based tests.
func setupCommandInfrastructure(t *testing.T, ctx context.Context, pubsubClient *pubsub.Client, cmdTopicID, cmdSubscriptionID, completionTopicID string) {
	t.Helper()
	cmdTopic, err := pubsubClient.CreateTopic(ctx, cmdTopicID)
	require.NoError(t, err)

	_, err = pubsubClient.CreateSubscription(ctx, cmdSubscriptionID, pubsub.SubscriptionConfig{
		Topic: cmdTopic,
	})
	require.NoError(t, err)

	completionTopic, err := pubsubClient.CreateTopic(ctx, completionTopicID)
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, completionTopic.Delete(ctx))
		require.NoError(t, cmdTopic.Delete(ctx))
		// Subscription is deleted with the topic
	})
}

// setupEnrichmentTestData creates devices and seeds Firestore for any test involving enrichment.
func setupEnrichmentTestData(
	t *testing.T,
	ctx context.Context,
	fsClient *firestore.Client,
	firestoreCollection string,
	runID string,
	numDevices int,
	rate float64,
) ([]*loadgen.Device, map[string]string, func()) {
	t.Helper()
	devices := make([]*loadgen.Device, numDevices)
	deviceToClientID := make(map[string]string)

	for i := 0; i < numDevices; i++ {
		deviceID := fmt.Sprintf("e2e-enrich-device-%d-%s", i, runID)
		clientID := fmt.Sprintf("client-for-%s", deviceID)
		deviceToClientID[deviceID] = clientID
		devices[i] = &loadgen.Device{ID: deviceID, MessageRate: rate, PayloadGenerator: &testPayloadGenerator{}}

		deviceDoc := map[string]interface{}{"clientID": clientID, "locationID": "loc-456", "deviceCategory": "cat-789"}
		_, err := fsClient.Collection(firestoreCollection).Doc(deviceID).Set(ctx, deviceDoc)
		require.NoError(t, err, "Failed to set device document for %s", deviceID)
	}

	cleanupFunc := func() {
		log.Info().Msg("Cleaning up Firestore documents from helper...")
		for _, device := range devices {
			_, err := fsClient.Collection(firestoreCollection).Doc(device.ID).Delete(context.Background())
			if err != nil {
				log.Warn().Err(err).Str("device_id", device.ID).Msg("Failed to cleanup firestore doc")
			}
		}
	}

	return devices, deviceToClientID, cleanupFunc
}

// startServiceDirector correctly initializes and starts the ServiceDirector for testing.
func startServiceDirector(t *testing.T, ctx context.Context, logger zerolog.Logger, arch *servicemanager.MicroserviceArchitecture) (*servicedirector.Director, string) {
	t.Helper()
	directorCfg := &servicedirector.Config{
		BaseConfig: microservice.BaseConfig{HTTPPort: ":0"},
	}
	director, err := servicedirector.NewServiceDirector(ctx, directorCfg, arch, logger)
	require.NoError(t, err)

	err = director.Start()
	require.NoError(t, err)

	baseURL := "http://127.0.0.1" + director.GetHTTPPort()
	return director, baseURL
}

// startIngestionService starts the refactored ingestion service.
func startIngestionService(t *testing.T, ctx context.Context, logger zerolog.Logger, directorURL, mqttURL, projectID, topicID, dataflowName string) microservice.Service {
	t.Helper()
	cfg := &ingestion.Config{
		BaseConfig:         microservice.BaseConfig{LogLevel: "debug", HTTPPort: ":0", ProjectID: projectID},
		ServiceName:        "ingestion-service-e2e",
		DataflowName:       dataflowName,
		ServiceDirectorURL: directorURL,
	}
	cfg.Producer.TopicID = topicID
	cfg.MQTT.BrokerURL = mqttURL
	cfg.MQTT.Topic = "devices/+/data"
	cfg.MQTT.ClientIDPrefix = "ingestion-e2e-"

	wrapper, err := ingestion.NewIngestionServiceWrapper(ctx, cfg, logger)
	require.NoError(t, err)

	go func() {
		if err := wrapper.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error().Err(err).Msg("IngestionService failed during test execution")
		}
	}()

	require.Eventually(t, func() bool {
		port := wrapper.GetHTTPPort()
		if port == "" || port == ":0" {
			return false
		}
		resp, err := http.Get(fmt.Sprintf("http://localhost%s/healthz", port))
		if err != nil {
			return false
		}
		defer func() {
			_ = resp.Body.Close()
		}()
		return resp.StatusCode == http.StatusOK
	}, 15*time.Second, 500*time.Millisecond, "IngestionService health check did not become OK")

	return wrapper
}

// startEnrichmentService starts the refactored enrichment service.
func startEnrichmentService(t *testing.T, ctx context.Context, logger zerolog.Logger, directorURL, projectID, subID, topicID, redisAddr, firestoreCollection, dataflowName string) microservice.Service {
	t.Helper()
	cfg := &enrichment.Config{
		BaseConfig:         microservice.BaseConfig{LogLevel: "debug", HTTPPort: ":0", ProjectID: projectID},
		ServiceName:        "enrichment-service-e2e",
		DataflowName:       dataflowName,
		ServiceDirectorURL: directorURL,
	}
	cfg.Consumer.SubscriptionID = subID
	cfg.ProducerConfig = &messagepipeline.GooglePubsubProducerConfig{TopicID: topicID}
	cfg.CacheConfig.RedisConfig.Addr = redisAddr
	cfg.CacheConfig.FirestoreConfig = &cache.FirestoreConfig{CollectionName: firestoreCollection, ProjectID: projectID}
	cfg.ProcessorConfig.NumWorkers = 5

	wrapper, err := enrichment.NewPublishMessageEnrichmentServiceWrapper(ctx, ctx, cfg, logger)
	require.NoError(t, err)

	go func() {
		if err := wrapper.Start(); err != nil && err != http.ErrServerClosed {
			logger.Error().Err(err).Msg("EnrichmentService failed")
		}
	}()
	return wrapper
}

// startBigQueryService is updated with the correct transformer logic.
func startBigQueryService(t *testing.T, ctx context.Context, logger zerolog.Logger, directorURL, projectID, subID, datasetID, tableID, dataflowName string) (microservice.Service, error) {
	t.Helper()
	cfg := &bigquery.Config{
		BaseConfig:         microservice.BaseConfig{LogLevel: "debug", HTTPPort: ":0", ProjectID: projectID},
		ServiceName:        "bigquery-service-e2e",
		DataflowName:       dataflowName,
		ServiceDirectorURL: directorURL,
	}
	cfg.Consumer.SubscriptionID = subID
	cfg.BigQueryConfig.DatasetID = datasetID
	cfg.BigQueryConfig.TableID = tableID
	cfg.BatchProcessing.BatchSize = 10
	cfg.BatchProcessing.NumWorkers = 2
	cfg.BatchProcessing.FlushTimeout = 5 * time.Second

	// --- THE FIX: Update the transformer to handle the new message structure ---
	transformer := func(msg types.ConsumedMessage) (*TestPayload, bool, error) {
		// 1. Unmarshal the outer message structure published by the IngestionService.
		var rawMsg ingestion.RawMessage
		if err := json.Unmarshal(msg.Payload, &rawMsg); err != nil {
			logger.Warn().Err(err).Msg("Failed to unmarshal outer RawMessage, skipping.")
			return nil, true, nil
		}

		// 2. Unmarshal the inner payload into the target BigQuery struct.
		var p TestPayload
		if err := json.Unmarshal(rawMsg.Payload, &p); err != nil {
			logger.Warn().Err(err).Msg("Failed to unmarshal inner payload for BQ, skipping.")
			return nil, true, nil
		}
		return &p, false, nil
	}
	// --- END FIX ---

	wrapper, err := bigquery.NewBQServiceWrapper[TestPayload](ctx, cfg, logger, transformer)
	if err != nil {
		return nil, err
	}

	go func() {
		if err := wrapper.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error().Err(err).Msg("BigQueryService failed")
		}
	}()
	return wrapper, nil
}

// startEnrichedBigQueryService is also updated with the correct transformer logic.
func startEnrichedBigQueryService(t *testing.T, ctx context.Context, logger zerolog.Logger, directorURL, projectID, subID, datasetID, tableID, dataflowName string) (microservice.Service, error) {
	t.Helper()
	cfg := &bigquery.Config{
		BaseConfig:         microservice.BaseConfig{LogLevel: "debug", HTTPPort: ":0", ProjectID: projectID},
		ServiceName:        "bigquery-enriched-service-e2e",
		DataflowName:       dataflowName,
		ServiceDirectorURL: directorURL,
	}
	cfg.Consumer.SubscriptionID = subID
	cfg.BigQueryConfig.DatasetID = datasetID
	cfg.BigQueryConfig.TableID = tableID
	cfg.BatchProcessing.BatchSize = 10
	cfg.BatchProcessing.NumWorkers = 2
	cfg.BatchProcessing.FlushTimeout = 5 * time.Second

	// UPDATED: The transformer now correctly extracts data from the generic EnrichmentData map.
	transformer := func(msg types.ConsumedMessage) (*EnrichedTestPayload, bool, error) {
		var enrichedMsg types.PublishMessage
		if err := json.Unmarshal(msg.Payload, &enrichedMsg); err != nil {
			logger.Warn().Err(err).Msg("Failed to unmarshal enriched publish message, skipping.")
			return nil, true, nil
		}

		var originalPayload TestPayload
		if err := json.Unmarshal(enrichedMsg.Payload, &originalPayload); err != nil {
			logger.Warn().Err(err).Msg("Failed to unmarshal inner original payload, skipping.")
			return nil, true, nil
		}

		p := &EnrichedTestPayload{
			DeviceID:  originalPayload.DeviceID,
			Timestamp: originalPayload.Timestamp,
			Value:     originalPayload.Value,
		}
		// Safely extract data from the map with type assertions.
		if enrichedMsg.EnrichmentData != nil {
			if name, ok := enrichedMsg.EnrichmentData["name"].(string); ok {
				p.ClientID = name
			}
			if location, ok := enrichedMsg.EnrichmentData["location"].(string); ok {
				p.LocationID = location
			}
			if category, ok := enrichedMsg.EnrichmentData["serviceTag"].(string); ok {
				p.Category = category
			}
		}
		return p, false, nil
	}

	wrapper, err := bigquery.NewBQServiceWrapper[EnrichedTestPayload](ctx, cfg, logger, transformer)
	if err != nil {
		return nil, err
	}

	go func() {
		if err := wrapper.Start(); err != nil && err != http.ErrServerClosed {
			logger.Error().Err(err).Msg("Enriched BigQueryService failed")
		}
	}()
	return wrapper, nil
}

// startIceStoreService starts an IceStore service for archiving payloads to GCS.
func startIceStoreService(t *testing.T, ctx context.Context, logger zerolog.Logger, directorURL, projectID, subID, bucketName, dataflowName string) microservice.Service {
	t.Helper()
	cfg := &icestore.Config{
		BaseConfig:         microservice.BaseConfig{LogLevel: "debug", HTTPPort: ":0", ProjectID: projectID},
		ServiceName:        "icestore-service-e2e",
		DataflowName:       dataflowName,
		ServiceDirectorURL: directorURL,
	}
	cfg.Consumer.SubscriptionID = subID
	cfg.IceStore.BucketName = bucketName
	cfg.IceStore.ObjectPrefix = "e2e-archive/"
	cfg.BatchProcessing.BatchSize = 10
	cfg.BatchProcessing.NumWorkers = 2
	cfg.BatchProcessing.FlushTimeout = 5 * time.Second

	wrapper, err := icestore.NewIceStoreServiceWrapper(ctx, cfg, logger)
	require.NoError(t, err)

	go func() {
		if err := wrapper.Start(); err != nil && err != http.ErrServerClosed {
			logger.Error().Err(err).Msg("IceStoreService failed")
		}
	}()
	return wrapper
}
