package lib

import (
	"context"
	"fmt"
	"log/slog"

	"cloud.google.com/go/pubsub"
	mqtt "github.com/mochi-mqtt/server/v2"
)

// PassthroughSubscriber manages a pull subscription to a Pub/Sub topic.
type PassthroughSubscriber struct {
	client *pubsub.Client
	server *mqtt.Server
	subID  string
}

// NewPassthroughSubscriber creates and configures a new Pub/Sub subscriber.
func NewPassthroughSubscriber(ctx context.Context, projectID, subID string, server *mqtt.Server) (*PassthroughSubscriber, error) {
	if projectID == "" || subID == "" {
		return nil, fmt.Errorf("GCP_PROJECT_ID and PUBSUB_SUBSCRIPTION_ID must be set to enable the Pub/Sub passthrough")
	}

	client, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to create pubsub client: %w", err)
	}

	slog.Info("Pub/Sub client created successfully", "project_id", projectID)

	return &PassthroughSubscriber{
		client: client,
		server: server,
		subID:  subID,
	}, nil
}

// Start begins receiving messages from the subscription in a blocking loop.
// It should be run in a goroutine. The provided context should be used to
// signal when to stop receiving.
func (s *PassthroughSubscriber) Start(ctx context.Context) {
	sub := s.client.Subscription(s.subID)
	slog.Info("Starting Pub/Sub subscriber", "subscription_id", s.subID)

	// Receive blocks until the context is cancelled or an unrecoverable error occurs.
	err := sub.Receive(ctx, func(ctx context.Context, msg *pubsub.Message) {
		// The target MQTT topic must be in the message attributes.
		topic, ok := msg.Attributes["mqttTopic"]
		if !ok || topic == "" {
			slog.Warn("Pub/Sub message missing 'mqttTopic' attribute, acknowledging and dropping", "message_id", msg.ID)
			msg.Ack() // Acknowledge the message so it's not redelivered.
			return
		}

		payload := msg.Data
		if payload == nil {
			payload = []byte{} // Mochi handles nil payloads, but this is safer.
		}

		// Publish the received payload directly to the in-memory Mochi server.
		if err := s.server.Publish(topic, payload, false, 0); err != nil {
			slog.Error("Failed to publish passthrough message to Mochi", "topic", topic, "error", err)
			msg.Nack() // Nack the message to signal that it should be redelivered.
			return
		}

		slog.Debug("Pub/Sub message passed through to MQTT", "topic", topic, "size_bytes", len(payload))
		msg.Ack() // Acknowledge the message after successful processing.
	})

	if err != nil {
		slog.Error("Pub/Sub Receive returned an error", "error", err)
	}

	slog.Info("Pub/Sub subscriber has stopped.")
}

// Close cleans up the Pub/Sub client resources.
func (s *PassthroughSubscriber) Close() error {
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}
