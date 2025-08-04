package lib

import (
	"context"
	"encoding/json"
	"github.com/rs/zerolog"
	"net/http"
	"time"

	"github.com/illmade-knight/go-test/loadgen" // Assuming this is your loadgen package path
	mqtt "github.com/mochi-mqtt/server/v2"
)

// Handler holds dependencies for the HTTP handlers, like a reference to the Mochi server.
type Handler struct {
	server *mqtt.Server
	logger zerolog.Logger
}

// NewHandler creates a new Handler.
func NewHandler(server *mqtt.Server) *Handler {
	return &Handler{server: server}
}

// --- Request/Response Structs ---

type LoadTestRequest struct {
	DurationSeconds int             `json:"duration_seconds"`
	TopicPattern    string          `json:"topic_pattern"`
	QoS             byte            `json:"qos"`
	Devices         []DeviceRequest `json:"devices"`
}

type DeviceRequest struct {
	ID               string  `json:"id"`
	MessageRateHz    float64 `json:"message_rate_hz"`
	PayloadGenerator string  `json:"payload_generator"`
	// Optional: For replay generator
	Messages [][]byte `json:"messages,omitempty"`
}

// PubSubMessage is the structure of a message from a Pub/Sub push subscription.
type PubSubMessage struct {
	Message struct {
		Data       []byte            `json:"data"`
		Attributes map[string]string `json:"attributes"`
		MessageID  string            `json:"messageId"`
	} `json:"message"`
	Subscription string `json:"subscription"`
}

// --- HTTP Handlers ---

// HandleLoadTest triggers a load test using the in-process loadgen library.
func (h *Handler) HandleLoadTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	var req LoadTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Basic validation
	if req.DurationSeconds <= 0 || req.TopicPattern == "" || len(req.Devices) == 0 {
		http.Error(w, "Bad request: duration, topic, and at least one device are required", http.StatusBadRequest)
		return
	}

	// Create the in-process client
	client := NewInProcessClient(h.server, req.TopicPattern, req.QoS)

	// Build the list of devices for the load generator
	var devices []*loadgen.Device
	for _, devReq := range req.Devices {
		generator, err := NewPayloadGenerator(devReq.PayloadGenerator, devReq.Messages)
		if err != nil {
			http.Error(w, "Bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		devices = append(devices, &loadgen.Device{
			ID:               devReq.ID,
			MessageRate:      devReq.MessageRateHz,
			PayloadGenerator: generator,
		})
	}

	// Run the load generator in a separate goroutine to avoid blocking the HTTP response.
	go func() {
		lg := loadgen.NewLoadGenerator(client, devices, h.logger)
		duration := time.Duration(req.DurationSeconds) * time.Second

		h.logger.Info().Dur("duration", duration).Int("devices", len(devices)).Msg("Starting load test")
		count, err := lg.Run(context.Background(), duration)
		if err != nil {
			h.logger.Error().Err(err).Msg("Load test finished with error")
		} else {
			h.logger.Info().Int("published", count).Msg("Load test finished successfully")
		}
	}()

	// Respond immediately to the caller.
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte("Load test accepted and started in the background.\n"))
}

// HandlePassthrough receives a Pub/Sub push message and publishes it directly to an MQTT topic.
func (h *Handler) HandlePassthrough(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	var msg PubSubMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "Bad request: could not decode Pub/Sub message", http.StatusBadRequest)
		h.logger.Error().Err(err).Msg("Failed to decode Pub/Sub message")
		return
	}

	// The target MQTT topic should be in the message attributes.
	topic, ok := msg.Message.Attributes["mqttTopic"]
	if !ok || topic == "" {
		http.Error(w, "Bad request: 'mqttTopic' attribute missing from Pub/Sub message", http.StatusBadRequest)
		return
	}

	payload := msg.Message.Data
	if payload == nil {
		payload = []byte{} // Allow empty payloads
	}

	// Publish directly to the in-memory server.
	if err := h.server.Publish(topic, payload, false, 0); err != nil {
		h.logger.Error().Err(err).Str("topic", topic).Msg("Passthrough failed to publish to Mochi")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	h.logger.Info().Str("topic", topic).Msg("Passthrough message published")
	w.WriteHeader(http.StatusOK)
}
