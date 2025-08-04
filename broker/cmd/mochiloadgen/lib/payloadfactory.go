package lib

import (
	"encoding/json"
	"fmt"
	"math/rand"

	"github.com/illmade-knight/go-test/loadgen" // Assuming this is your loadgen package path
)

// NewPayloadGenerator is a factory function that returns a payload generator
// based on a string identifier.
func NewPayloadGenerator(name string, messages [][]byte) (loadgen.PayloadGenerator, error) {
	switch name {
	case "gardenMonitor":
		return NewGardenMonitorPayloadGenerator(), nil
	case "replay":
		if len(messages) == 0 {
			return nil, fmt.Errorf("payload generator 'replay' requires non-empty 'messages' field")
		}
		return loadgen.NewReplayPayloadGenerator(messages), nil
	default:
		return nil, fmt.Errorf("unknown payload generator: %s", name)
	}
}

// --- Concrete Payload Generator Implementations ---
// This is an example based on your helpers_test.go file.

// GardenMonitorPayload represents the specific payload for the garden monitor devices.
type GardenMonitorPayload struct {
	DE           string `json:"de"`
	SIM          string `json:"sim"`
	RSSI         string `json:"rssi"`
	Version      string `json:"version"`
	Sequence     int    `json:"sequence"`
	Battery      int    `json:"battery"`
	Temperature  int    `json:"temperature"`
	Humidity     int    `json:"humidity"`
	SoilMoisture int    `json:"soil_moisture"`
}

// GardenMonitorPayloadGenerator implements the lib.PayloadGenerator interface.
type GardenMonitorPayloadGenerator struct {
	state deviceState
}

// deviceState holds the dynamic state for a single simulated garden monitor.
type deviceState struct {
	Sequence     int
	Battery      int
	Temperature  int
	Humidity     int
	SoilMoisture int
	RSSI         int
}

// NewGardenMonitorPayloadGenerator creates a new generator for garden monitor payloads.
func NewGardenMonitorPayloadGenerator() *GardenMonitorPayloadGenerator {
	return &GardenMonitorPayloadGenerator{
		state: deviceState{
			Sequence:     0,
			Battery:      rand.Intn(21) + 80,
			Temperature:  rand.Intn(15) + 10,
			Humidity:     rand.Intn(30) + 40,
			SoilMoisture: rand.Intn(400) + 300,
			RSSI:         (rand.Intn(40) + 50) * -1,
		},
	}
}

// GeneratePayload creates the next payload for a garden monitor device, updating its state.
func (g *GardenMonitorPayloadGenerator) GeneratePayload(device *loadgen.Device) ([]byte, error) {
	// Update device state for the next message
	g.state.Sequence++
	if g.state.Battery > 10 {
		g.state.Battery -= rand.Intn(2)
	}
	g.state.Temperature += rand.Intn(3) - 1
	g.state.Humidity += rand.Intn(5) - 2
	g.state.SoilMoisture += rand.Intn(41) - 20
	if g.state.SoilMoisture < 100 {
		g.state.SoilMoisture = 100
	}
	if g.state.SoilMoisture > 900 {
		g.state.SoilMoisture = 900
	}

	// Create the payload from the new state
	payload := GardenMonitorPayload{
		DE:           device.ID, // Use the device ID from the context
		SIM:          fmt.Sprintf("SIM_LOAD_%s", device.ID[len(device.ID)-4:]),
		RSSI:         fmt.Sprintf("%ddBm", g.state.RSSI),
		Version:      "2.0.0-unified",
		Sequence:     g.state.Sequence,
		Battery:      g.state.Battery,
		Temperature:  g.state.Temperature,
		Humidity:     g.state.Humidity,
		SoilMoisture: g.state.SoilMoisture,
	}

	return json.Marshal(payload)
}
