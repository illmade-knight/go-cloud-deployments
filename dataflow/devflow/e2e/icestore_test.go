//go:build integration

// Package e2e contains end-to-end tests for dataflow pipelines.
// This test file, icestore_test.go, validates the dataflow from MQTT ingestion
// directly to Google Cloud Storage archival (the "icestore" sink).
package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"github.com/google/uuid"
	"github.com/illmade-knight/go-cloud-manager/pkg/servicemanager"
	"github.com/illmade-knight/go-dataflow-services/pkg/ingestion"
	"github.com/illmade-knight/go-test/auth"
	"github.com/illmade-knight/go-test/emulators"
	"github.com/illmade-knight/go-test/loadgen"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

const (
	generateIcestoreMessagesFor = 3 * time.Second
	icestoreLoadTestNumDevices  = 2
	icestoreLoadTestRate        = 5.0
)

func TestIceStoreDataflowE2E(t *testing.T) {
	// --- Logger and Prerequisite Checks ---
	logger := zerolog.New(os.Stderr).With().Timestamp().Str("test", "TestIceStoreDataflowE2E").Logger()

	projectID := auth.CheckGCPAuth(t)

	// --- Timing & Metrics Setup ---
	timings := make(map[string]string)
	testStart := time.Now()
	var publishedCount int
	var verifiedCount int
	var expectedMessageCount int

	t.Cleanup(func() {
		timings["TotalTestDuration"] = time.Since(testStart).String()
		timings["MessagesExpected"] = strconv.Itoa(expectedMessageCount)
		timings["MessagesPublished(Actual)"] = strconv.Itoa(publishedCount)
		timings["MessagesVerified(Actual)"] = strconv.Itoa(verifiedCount)

		logger.Info().Msg("\n--- Test Timing & Metrics Breakdown ---")
		for name, d := range timings {
			logger.Info().Msgf("%-35s: %s", name, d)
		}
		logger.Info().Msg("------------------------------------")
	})

	totalTestContext, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	// 1. Define unique resources for this test run.
	runID := uuid.New().String()[:8]
	dataflowName := fmt.Sprintf("icestore-flow-%s", runID)
	ingestionOutputTopicID := fmt.Sprintf("icestore-ingestion-topic-%s", runID)
	uniqueIcestoreSubID := fmt.Sprintf("icestore-sub-%s", runID)
	uniqueBucketName := fmt.Sprintf("sm-icestore-bucket-%s", runID)
	logger.Info().Str("run_id", runID).Msg("Generated unique resources for test run")

	// 2. Build the services definition in memory.
	servicesConfig := &servicemanager.MicroserviceArchitecture{
		Environment: servicemanager.Environment{
			Name:      "e2e-icestore",
			ProjectID: projectID,
			Location:  "US",
		},
		Dataflows: map[string]servicemanager.ResourceGroup{
			dataflowName: {
				Name:      dataflowName,
				Lifecycle: &servicemanager.LifecyclePolicy{Strategy: servicemanager.LifecycleStrategyEphemeral},
				Resources: servicemanager.CloudResourcesSpec{
					Topics:        []servicemanager.TopicConfig{{CloudResource: servicemanager.CloudResource{Name: ingestionOutputTopicID}}},
					Subscriptions: []servicemanager.SubscriptionConfig{{CloudResource: servicemanager.CloudResource{Name: uniqueIcestoreSubID}, Topic: ingestionOutputTopicID}},
					GCSBuckets:    []servicemanager.GCSBucket{{CloudResource: servicemanager.CloudResource{Name: uniqueBucketName}, Location: "US"}},
				},
			},
		},
	}

	// 3. Setup dependencies.
	var opts []option.ClientOption
	if creds := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); creds != "" {
		opts = append(opts, option.WithCredentialsFile(creds))
	}
	start := time.Now()
	mqttConnInfo := emulators.SetupMosquittoContainer(t, totalTestContext, emulators.GetDefaultMqttImageContainer())
	timings["EmulatorSetup(MQTT)"] = time.Since(start).String()

	gcsClient, err := storage.NewClient(totalTestContext, opts...)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, gcsClient.Close())
	})

	// 4. Start ServiceDirector and orchestrate resources.
	start = time.Now()
	directorService, directorURL := startServiceDirector(t, totalTestContext, logger.With().Str("service", "servicedirector").Logger(), servicesConfig)
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		directorService.Shutdown(shutdownCtx)
	})
	timings["ServiceStartup(Director)"] = time.Since(start).String()

	start = time.Now()
	setupURL := directorURL + "/dataflow/setup"
	resp, err := http.Post(setupURL, "application/json", bytes.NewBuffer([]byte{}))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()
	timings["CloudResourceSetup(Director)"] = time.Since(start).String()

	t.Cleanup(func() {
		teardownStart := time.Now()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()

		logger.Info().Msg("Requesting resource teardown from ServiceDirector...")
		teardownURL := directorURL + "/orchestrate/teardown"
		req, _ := http.NewRequestWithContext(cleanupCtx, http.MethodPost, teardownURL, nil)
		_, err = http.DefaultClient.Do(req)
		if err != nil {
			logger.Warn().Err(err).Msg("Teardown call to director failed")
		}

		logger.Info().Str("bucket", uniqueBucketName).Msg("Ensuring GCS bucket is deleted directly as a fallback.")
		bucket := gcsClient.Bucket(uniqueBucketName)
		it := bucket.Objects(cleanupCtx, nil)
		for {
			attrs, err := it.Next()
			if errors.Is(err, iterator.Done) {
				break
			}
			if err == nil {
				err = bucket.Object(attrs.Name).Delete(cleanupCtx)
				if err != nil {
					logger.Warn().Err(err).Str("bucket", uniqueBucketName).Msg("Failed to delete GCS bucket object.")
				}
			}
		}
		err = bucket.Delete(cleanupCtx)
		if err != nil {
			logger.Warn().Err(err).Str("bucket", uniqueBucketName).Msg("Failed to delete GCS bucket.")
		}
		timings["CloudResourceTeardown"] = time.Since(teardownStart).String()
	})

	// 5. Start services
	start = time.Now()
	cfg := ingestion.LoadConfigDefaults(projectID)
	cfg.DataflowName = dataflowName
	cfg.ServiceDirectorURL = directorURL
	cfg.MQTT.BrokerURL = mqttConnInfo.EmulatorAddress
	cfg.OutputTopicID = ingestionOutputTopicID
	ingestionLogger := logger.With().Str("service", "ingestion").Logger()
	ingestionSvc := startIngestionService(t, totalTestContext, ingestionLogger, cfg)
	timings["ServiceStartup(Ingestion)"] = time.Since(start).String()
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = ingestionSvc.Shutdown(shutdownCtx)
	})

	start = time.Now()
	iceLogger := logger.With().Str("service", "icestore").Logger()
	icestoreSvc := startIceStoreService(t, totalTestContext, iceLogger, projectID, uniqueIcestoreSubID, uniqueBucketName, dataflowName)
	timings["ServiceStartup(IceStore)"] = time.Since(start).String()
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = icestoreSvc.Shutdown(shutdownCtx)
	})
	logger.Info().Msg("All services started successfully.")

	// 6. Run Load Generator.
	loadgenStart := time.Now()
	logger.Info().Msg("Starting MQTT load generator...")
	loadgenClient := loadgen.NewMqttClient(mqttConnInfo.EmulatorAddress, "devices/+/data", 1, logger)

	devices := make([]*loadgen.Device, icestoreLoadTestNumDevices)
	for i := 0; i < icestoreLoadTestNumDevices; i++ {
		devices[i] = &loadgen.Device{ID: fmt.Sprintf("e2e-icestore-device-%d-%s", i, runID), MessageRate: icestoreLoadTestRate, PayloadGenerator: &testPayloadGenerator{}}
	}

	generator := loadgen.NewLoadGenerator(loadgenClient, devices, logger)
	expectedMessageCount = generator.ExpectedMessagesForDuration(generateIcestoreMessagesFor)
	publishedCount, err = generator.Run(totalTestContext, generateIcestoreMessagesFor)
	require.NoError(t, err)
	timings["LoadGeneration"] = time.Since(loadgenStart).String()
	logger.Info().Int("published_count", publishedCount).Msg("Load generator finished.")

	// 7. Verify results.
	verificationStart := time.Now()
	gcsClientForVerification, err := storage.NewClient(totalTestContext, opts...)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, gcsClientForVerification.Close())
	})

	verifyGCSResults(t, logger, totalTestContext, gcsClientForVerification, uniqueBucketName, publishedCount)
	timings["VerificationDuration"] = time.Since(verificationStart).String()
	timings["ProcessingAndVerificationLatency"] = time.Since(loadgenStart).String()
	verifiedCount = publishedCount
}
