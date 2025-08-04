package lib

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/illmade-knight/go-test/loadgen" // Assuming this is your loadgen package path
	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/packets"
)

// InProcessClient implements the loadgen.Client interface for direct,
// in-memory communication with a Mochi server instance.
type InProcessClient struct {
	server       *mqtt.Server
	topicPattern string
	qos          byte
}

// NewInProcessClient creates a new client that publishes directly to the server.
func NewInProcessClient(server *mqtt.Server, topicPattern string, qos byte) loadgen.Client {
	return &InProcessClient{
		server:       server,
		topicPattern: topicPattern,
		qos:          qos,
	}
}

// Connect is a no-op because no network connection is needed.
func (c *InProcessClient) Connect() error {
	slog.Debug("InProcessClient: Connect() called (no-op)")
	return nil
}

// Disconnect is a no-op because the server lifecycle is managed externally.
func (c *InProcessClient) Disconnect() {
	slog.Debug("InProcessClient: Disconnect() called (no-op)")
}

// Publish generates a payload and publishes it directly to the server's internal bus.
func (c *InProcessClient) Publish(ctx context.Context, device *loadgen.Device) (bool, error) {
	// Check if the context has been cancelled before doing work.
	if err := ctx.Err(); err != nil {
		return false, err
	}

	payloadBytes, err := device.PayloadGenerator.GeneratePayload(device)
	if err != nil {
		return false, fmt.Errorf("failed to generate payload for device %s: %w", device.ID, err)
	}

	topic := strings.Replace(c.topicPattern, "+", device.ID, 1)

	// Create the MQTT packet to be published.
	pk := packets.Packet{
		FixedHeader: packets.FixedHeader{
			Type: packets.Publish,
			Qos:  c.qos,
		},
		TopicName: topic,
		Payload:   payloadBytes,
	}

	// Publish directly to the server. This method bypasses the network stack.
	// The server will distribute it to any subscribed clients (if any).
	if err := c.server.Publish(pk.TopicName, pk.Payload, false, c.qos); err != nil {
		slog.Error("InProcessClient: publish failed", "topic", pk.TopicName, "error", err)
		return false, err
	}

	slog.Debug("InProcessClient: message published", "device_id", device.ID, "topic", topic)
	return true, nil
}
