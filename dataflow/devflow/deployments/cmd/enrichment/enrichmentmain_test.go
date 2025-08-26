package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestResourcesYAMLParsing validates that the embedded resources.yaml file
// has the correct structure and content needed by the enrichment service.
// This acts as a pre-flight check to ensure the service binary is built with a
// valid configuration before deployment.
func TestResourcesYAMLParsing(t *testing.T) {
	// --- Arrange ---
	// The `//go:embed resources.yaml` directive in the main enrichmentmain.go file
	// will automatically load the resources.yaml from this directory during testing.
	// This test validates that the file is parseable and contains the expected resources.

	// --- Act ---
	var resourceCfg serviceConfig
	err := yaml.Unmarshal(resourcesYAML, &resourceCfg)

	// --- Assert ---
	// 1. First, ensure the YAML is well-formed and can be parsed into our struct.
	require.NoError(t, err, "should be able to parse the embedded resources.yaml")

	// 2. Next, validate the structure by checking the number of expected resources.
	//    This mirrors the validation checks in the main() function.
	assert.Len(t, resourceCfg.Subscriptions, 1, "expected exactly one subscription to be defined")
	assert.Len(t, resourceCfg.Topics, 1, "expected exactly one topic to be defined")
	assert.Len(t, resourceCfg.FirestoreDatabases, 1, "expected exactly one firestore database to be defined")
	assert.Len(t, resourceCfg.FirestoreCollections, 1, "expected exactly one firestore collection to be defined")

	// 3. Finally, assert the content of the YAML to ensure the correct names are present.
	if len(resourceCfg.Topics) == 1 {
		assert.Equal(t, "enrichment-out", resourceCfg.Topics[0].Name)
	}
	if len(resourceCfg.Subscriptions) == 1 {
		assert.Equal(t, "bq-ingestion", resourceCfg.Subscriptions[0].Name)
	}
	if len(resourceCfg.FirestoreCollections) == 1 {
		assert.Equal(t, "devices", resourceCfg.FirestoreCollections[0].Name)
	}

	t.Log("✅ resources.yaml was parsed successfully with the correct structure and values.")
}
