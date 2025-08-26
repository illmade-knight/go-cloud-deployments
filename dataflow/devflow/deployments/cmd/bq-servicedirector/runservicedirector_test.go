package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDerivePubSubConfigFromResources validates that the embedded resources.yaml
// file can be correctly parsed and that the expected configuration values are derived.
// This acts as a pre-flight check to ensure the service binary is built with a
// valid configuration before deployment.
func TestDerivePubSubConfigFromResources(t *testing.T) {
	// --- Arrange ---
	// REFACTOR: These expected values are now updated to match the logical names
	// defined in the main services.yaml and the derivation logic in the Conductor.
	expectedCommandTopic := "command-topic"
	expectedCommandSub := "command-topic-sub" // Derived from the topic name
	expectedCompletionTopic := "completion-topic"

	// --- Act ---
	// Call the function we want to test.
	config, err := derivePubSubConfigFromResources()

	// --- Assert ---
	// 1. Ensure no errors occurred during parsing.
	require.NoError(t, err)
	require.NotNil(t, config)

	// 2. Assert that the derived values match our expectations.
	assert.Equal(t, expectedCompletionTopic, config.CompletionTopicID)
	assert.Equal(t, expectedCommandSub, config.CommandSubID)
	assert.Equal(t, expectedCommandTopic, config.CommandTopicID)

	t.Log("✅ resources.yaml was parsed successfully with the correct values.")
}
