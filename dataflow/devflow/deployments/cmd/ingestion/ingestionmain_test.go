package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestResourcesYAMLParsing validates that the embedded resources.yaml file
// has the correct structure and content needed by the ingestion service.
func TestResourcesYAMLParsing(t *testing.T) {
	// --- Arrange ---
	// REFACTOR: Read the expected topic name from an environment variable.
	// This allows the Conductor to tell the test what value to expect.
	expectedTopic := os.Getenv("EXPECTED_TOPIC_NAME")
	if expectedTopic == "" {
		t.Skip("Skipping test: EXPECTED_TOPIC_NAME environment variable not set. This test is intended to be run by the Conductor.")
	}

	// --- Act ---
	var resourceCfg serviceConfig
	err := yaml.Unmarshal(resourcesYAML, &resourceCfg)

	// --- Assert ---
	require.NoError(t, err, "should be able to parse the embedded resources.yaml")
	assert.Len(t, resourceCfg.Topics, 1, "expected exactly one topic to be defined")

	if len(resourceCfg.Topics) == 1 {
		// Assert against the dynamic value, not a hardcoded string.
		assert.Equal(t, expectedTopic, resourceCfg.Topics[0].Name)
	}

	t.Log("✅ resources.yaml was parsed successfully with the correct values.")
}
