package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestResourcesYAMLParsing validates that the embedded resources.yaml file
// has the correct structure and content needed by the icestore service.
// This acts as a pre-flight check to ensure the service binary is built with a
// valid configuration before deployment.
func TestResourcesYAMLParsing(t *testing.T) {
	// --- Arrange ---
	// The //go:embed directive in icestoremain.go loads the local resources.yaml
	// at test time. We define the expected values from that file here.
	expectedSubscription := "icestore-subscription"
	expectedBucket := "icestore-bucket"

	// --- Act ---
	var resourceCfg serviceConfig
	err := yaml.Unmarshal(resourcesYAML, &resourceCfg)

	// --- Assert ---
	// 1. Ensure the YAML is well-formed and can be parsed into our struct.
	require.NoError(t, err, "should be able to parse the embedded resources.yaml")

	// 2. Validate the structure by checking for the expected number of resources,
	//    mirroring the checks in the main() function.
	assert.Len(t, resourceCfg.Subscriptions, 1, "expected exactly one subscription to be defined")
	assert.Len(t, resourceCfg.GCSBuckets, 1, "expected exactly one GCS bucket to be defined")

	// 3. Assert the content of the YAML to ensure the correct names are present.
	if len(resourceCfg.Subscriptions) == 1 {
		assert.Equal(t, expectedSubscription, resourceCfg.Subscriptions[0].Name)
	}
	if len(resourceCfg.GCSBuckets) == 1 {
		assert.Equal(t, expectedBucket, resourceCfg.GCSBuckets[0].Name)
	}

	t.Log("✅ resources.yaml was parsed successfully with the correct structure and values.")
}
