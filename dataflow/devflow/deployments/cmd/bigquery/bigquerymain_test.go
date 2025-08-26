package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestResourcesYAMLParsing validates that the embedded resources.yaml file
// has the correct structure and content needed by the BigQuery service.
func TestResourcesYAMLParsing(t *testing.T) {
	// --- Arrange ---
	// REFACTOR: Updated these expected values to match the actual names
	// defined in the main services.yaml file.
	expectedSubscription := "bq-ingestion"
	expectedDataset := "iot_data_bq"
	expectedTable := "ingested_payloads_bq"

	// --- Act ---
	var resourceCfg serviceConfig
	err := yaml.Unmarshal(resourcesYAML, &resourceCfg)

	// --- Assert ---
	require.NoError(t, err, "should be able to parse the embedded resources.yaml")

	assert.Len(t, resourceCfg.Subscriptions, 1, "expected exactly one subscription to be defined")
	assert.Len(t, resourceCfg.BigQueryDatasets, 1, "expected exactly one BigQuery dataset to be defined")
	assert.Len(t, resourceCfg.BigQueryTables, 1, "expected exactly one BigQuery table to be defined")

	if len(resourceCfg.Subscriptions) == 1 {
		assert.Equal(t, expectedSubscription, resourceCfg.Subscriptions[0].Name)
	}
	if len(resourceCfg.BigQueryDatasets) == 1 {
		assert.Equal(t, expectedDataset, resourceCfg.BigQueryDatasets[0].Name)
	}
	if len(resourceCfg.BigQueryTables) == 1 {
		assert.Equal(t, expectedTable, resourceCfg.BigQueryTables[0].Name)
	}

	t.Log("✅ resources.yaml was parsed successfully with the correct structure and values.")
}
