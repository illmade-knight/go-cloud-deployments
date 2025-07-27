//go:build integration

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"google.golang.org/api/iterator"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	bq "cloud.google.com/go/bigquery"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"

	"github.com/illmade-knight/go-cloud-manager/microservice"
	"github.com/illmade-knight/go-cloud-manager/microservice/servicedirector"
	"github.com/illmade-knight/go-cloud-manager/pkg/servicemanager"
	"github.com/illmade-knight/go-test/emulators"
	"github.com/illmade-knight/go-test/loadgen"
)

const (
	generateSimpleBigqueryMessagesFor = 5 * time.Second
	fullBigQueryTestNumDevices        = 5
	fullBigQueryTestRate              = 2.0
)

func TestFullDataflowE2E(t *testing.T) {
	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		t.Skip("Skipping E2E test: GOOGLE_CLOUD_PROJECT env var must be set.")
	}
	if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" {
		log.Warn().Msg("GOOGLE_APPLICATION_CREDENTIALS not set, relying on Application Default Credentials (ADC).")
		checkGCPAuth(t)
	}

	timings := make(map[string]string)
	testStart := time.Now()
	var publishedCount int
	var expectedMessageCount int

	t.Cleanup(func() {
		timings["TotalTestDuration"] = time.Since(testStart).String()
		timings["MessagesExpected"] = strconv.Itoa(expectedMessageCount)
		timings["MessagesPublished(Actual)"] = strconv.Itoa(publishedCount)
		timings["MessagesVerified(Actual)"] = strconv.Itoa(publishedCount)

		t.Log("\n--- Test Timing & Metrics Breakdown ---")
		for name, d := range timings {
			t.Logf("%-35s: %s", name, d)
		}
		t.Log("------------------------------------")
	})

	totalTestContext, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	logger := log.With().Str("test", "TestFullDataflowE2E").Logger()

	runID := uuid.New().String()[:8]
	dataflowName := fmt.Sprintf("bq-flow-%s", runID)
	uniqueTopicID := fmt.Sprintf("dev-ingestion-topic-%s", runID)
	uniqueSubID := fmt.Sprintf("dev-bq-subscription-%s", runID)
	uniqueDatasetID := fmt.Sprintf("dev_dataflow_dataset_%s", runID)
	uniqueTableID := fmt.Sprintf("dev_ingested_payloads_%s", runID)
	logger.Info().Str("run_id", runID).Msg("Generated unique resources for test run")

	schemaIdentifier := "github.com/illmade-knight/go-iot-dataflows/dataflow/devflow/e2e.TestPayload"
	servicemanager.RegisterSchema(schemaIdentifier, TestPayload{})
	servicesConfig := &servicemanager.MicroserviceArchitecture{
		Environment: servicemanager.Environment{
			Name:      "e2e",
			ProjectID: projectID,
			Location:  "US",
		},
		Dataflows: map[string]servicemanager.ResourceGroup{
			dataflowName: {
				Name:      dataflowName,
				Lifecycle: &servicemanager.LifecyclePolicy{Strategy: servicemanager.LifecycleStrategyEphemeral},
				Resources: servicemanager.CloudResourcesSpec{
					Topics:           []servicemanager.TopicConfig{{CloudResource: servicemanager.CloudResource{Name: uniqueTopicID}}},
					Subscriptions:    []servicemanager.SubscriptionConfig{{CloudResource: servicemanager.CloudResource{Name: uniqueSubID}, Topic: uniqueTopicID}},
					BigQueryDatasets: []servicemanager.BigQueryDataset{{CloudResource: servicemanager.CloudResource{Name: uniqueDatasetID}}},
					BigQueryTables: []servicemanager.BigQueryTable{
						{
							CloudResource:    servicemanager.CloudResource{Name: uniqueTableID},
							Dataset:          uniqueDatasetID,
							SchemaType:       schemaIdentifier,
							ClusteringFields: []string{"device_id"},
						},
					},
				},
			},
		},
	}

	var opts []option.ClientOption
	if creds := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); creds != "" {
		opts = append(opts, option.WithCredentialsFile(creds))
	}
	bqClient, err := bq.NewClient(totalTestContext, projectID, opts...)
	require.NoError(t, err)
	defer bqClient.Close()

	start := time.Now()
	mqttContainer := emulators.SetupMosquittoContainer(t, totalTestContext, emulators.GetDefaultMqttImageContainer())
	timings["EmulatorSetup(MQTT)"] = time.Since(start).String()

	start = time.Now()
	directorService, directorURL := startServiceDirector(t, totalTestContext, logger.With().Str("service", "servicedirector").Logger(), servicesConfig)
	t.Cleanup(directorService.Shutdown)
	timings["ServiceStartup(Director)"] = time.Since(start).String()

	start = time.Now()
	setupURL := directorURL + "/dataflow/setup"
	dataflowRequest := servicedirector.OrchestrateRequest{DataflowName: "all"}
	body, err := json.Marshal(dataflowRequest)
	require.NoError(t, err)
	resp, err := http.Post(setupURL, "application/json", bytes.NewBuffer(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "Director setup call should succeed")
	require.NoError(t, resp.Body.Close())
	timings["CloudResourceSetup(Director)"] = time.Since(start).String()

	t.Cleanup(func() {
		teardownStart := time.Now()
		logger.Info().Msg("Requesting resource teardown from ServiceDirector...")
		teardownURL := directorURL + "/orchestrate/teardown"
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, teardownURL, nil)
		_, err = http.DefaultClient.Do(req)
		if err != nil {
			logger.Warn().Err(err).Msg("cleanup call failed")
		}
		ds := bqClient.Dataset(uniqueDatasetID)
		if err := ds.DeleteWithContents(context.Background()); err != nil {
			logger.Warn().Err(err).Str("dataset", uniqueDatasetID).Msg("Failed to delete BigQuery dataset during cleanup.")
		}
		timings["CloudResourceTeardown"] = time.Since(teardownStart).String()
	})

	start = time.Now()
	ingestionSvc := startIngestionService(t, totalTestContext, logger.With().Str("service", "ingestion").Logger(), directorURL, mqttContainer.EmulatorAddress, projectID, uniqueTopicID, dataflowName)
	timings["ServiceStartup(Ingestion)"] = time.Since(start).String()
	t.Cleanup(ingestionSvc.Shutdown)

	start = time.Now()
	// --- THE FIX ---
	// Declare the error variable and correctly assign the service to bqSvc.
	var bqSvc microservice.Service
	var bqSvcErr error
	require.Eventually(t, func() bool {
		bqSvc, bqSvcErr = startBigQueryService(t, totalTestContext, logger.With().Str("service", "bigquery").Logger(), directorURL, projectID, uniqueSubID, uniqueDatasetID, uniqueTableID, dataflowName)
		return bqSvcErr == nil
	}, 30*time.Second, 5*time.Second, "BigQuery service failed to start")
	// --- END FIX ---
	timings["ServiceStartup(BigQuery)"] = time.Since(start).String()
	t.Cleanup(bqSvc.Shutdown)
	logger.Info().Msg("All services started successfully.")

	loadgenStart := time.Now()
	logger.Info().Msg("Starting MQTT load generator...")
	loadgenClient := loadgen.NewMqttClient(mqttContainer.EmulatorAddress, "devices/%s/data", 1, logger)
	devices := make([]*loadgen.Device, fullBigQueryTestNumDevices)
	for i := 0; i < fullBigQueryTestNumDevices; i++ {
		devices[i] = &loadgen.Device{ID: fmt.Sprintf("e2e-bq-device-%d-%s", i, runID), MessageRate: fullBigQueryTestRate, PayloadGenerator: &testPayloadGenerator{}}
	}
	generator := loadgen.NewLoadGenerator(loadgenClient, devices, logger)
	expectedMessageCount = generator.ExpectedMessagesForDuration(generateSimpleBigqueryMessagesFor)

	publishedCount, err = generator.Run(totalTestContext, generateSimpleBigqueryMessagesFor)
	require.NoError(t, err)
	timings["LoadGeneration"] = time.Since(loadgenStart).String()
	logger.Info().Int("published_count", publishedCount).Msg("Load generator finished.")

	verificationStart := time.Now()
	logger.Info().Msg("Starting BigQuery verification...")

	countValidator := func(t *testing.T, iter *bq.RowIterator) error {
		var rowCount int
		for {
			var row map[string]bq.Value
			err := iter.Next(&row)
			if errors.Is(err, iterator.Done) {
				break
			}
			if err != nil {
				return err
			}
			rowCount++
		}
		require.Equal(t, publishedCount, rowCount, "the final number of rows in BigQuery should match the number of messages published")
		return nil
	}

	verifyBigQueryRows(t, logger, totalTestContext, projectID, uniqueDatasetID, uniqueTableID, publishedCount, countValidator)

	timings["VerificationDuration"] = time.Since(verificationStart).String()
	timings["ProcessingAndVerificationLatency"] = time.Since(loadgenStart).String()
}
