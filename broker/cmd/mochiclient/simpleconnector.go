package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"strings"
	"syscall"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"golang.org/x/term"
)

// onConnectHandler is a callback that prints a confirmation message upon connecting.
var onConnectHandler mqtt.OnConnectHandler = func(client mqtt.Client) {
	log.Println("Connected to MQTT Broker!")
}

// onConnectionLostHandler is a callback that prints an error message upon losing a connection.
var onConnectionLostHandler mqtt.ConnectionLostHandler = func(client mqtt.Client, err error) {
	log.Printf("Connection Lost: %v", err)
}

func main() {
	// --- Configuration: Define flags for command-line arguments ---
	brokerURL := flag.String("broker", "", "MQTT broker URL (e.g., tcp://host:port) (required)")
	topic := flag.String("topic", "devices/local/events", "MQTT topic to publish to")
	username := flag.String("user", "", "Username for MQTT broker (required)")
	password := flag.String("pass", "", "Password for MQTT broker (overrides -secret and prompt)")
	secretName := flag.String("secret", "", "Google Secret Manager name (e.g., projects/p/s/v/l)")
	flag.Parse()

	// --- Basic Validation ---
	if *brokerURL == "" || *username == "" {
		log.Println("ERROR: The -broker and -user flags are required.")
		flag.Usage()
		return
	}

	finalBrokerURL := ensurePort(*brokerURL)

	// --- Determine Password Source ---
	finalPassword, err := getFinalPassword(*password, *secretName)
	if err != nil {
		log.Fatalf("Failed to get password: %v", err)
	}

	// --- Configure MQTT Client Options ---
	opts := mqtt.NewClientOptions()
	opts.AddBroker(finalBrokerURL)
	opts.SetClientID(fmt.Sprintf("local-go-client-%d", time.Now().Unix()))
	opts.SetUsername(*username)
	opts.SetPassword(finalPassword)
	opts.SetKeepAlive(30 * time.Second)
	opts.OnConnect = onConnectHandler
	opts.OnConnectionLost = onConnectionLostHandler

	// --- Connect, Publish, and Disconnect ---
	log.Printf("Attempting to connect to %s...", *brokerURL)
	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.WaitTimeout(10*time.Second) && token.Error() != nil {
		log.Fatalf("Failed to connect: %v", token.Error())
	}

	payload := fmt.Sprintf(`{"timestamp": %d, "message": "Hello from secure client!"}`, time.Now().Unix())
	log.Printf("Publishing message to topic '%s'...", *topic)
	token := client.Publish(*topic, 0, false, payload)
	token.Wait()

	if token.Error() != nil {
		log.Printf("Failed to publish: %v", token.Error())
	} else {
		log.Println("✅ Message published successfully!")
	}

	client.Disconnect(500)
	log.Println("Client disconnected.")
}

// ensurePort checks if a URL has a port and adds a default if it doesn't.
func ensurePort(brokerURL string) string {
	// We only need to check the part after the scheme
	parts := strings.SplitN(brokerURL, "://", 2)
	if len(parts) != 2 {
		return brokerURL // Not a valid URL format, return as-is
	}
	scheme := parts[0]
	hostPart := parts[1]

	_, _, err := net.SplitHostPort(hostPart)
	if err == nil {
		return brokerURL // Port already exists
	}

	// Port is missing, add a default based on the scheme
	var defaultPort string
	switch strings.ToLower(scheme) {
	case "tls", "ssl":
		defaultPort = "8883"
	default:
		defaultPort = "1883"
	}

	log.Printf("Port not found in broker URL, adding default port %s", defaultPort)
	return fmt.Sprintf("%s:%s", brokerURL, defaultPort)
}

// getFinalPassword determines which password to use based on flags.
func getFinalPassword(passFlag, secretFlag string) (string, error) {
	if passFlag != "" {
		log.Println("Using password from -pass flag.")
		return passFlag, nil
	}
	if secretFlag != "" {
		log.Println("Using password from Google Secret Manager...")
		return accessSecretVersion(secretFlag)
	}
	log.Println("No password or secret flag provided.")
	return promptForPassword()
}

// promptForPassword securely asks the user for a password from the terminal.
func promptForPassword() (string, error) {
	fmt.Print("Enter Password: ")
	bytePassword, err := term.ReadPassword(int(syscall.Stdin))
	if err != nil {
		return "", fmt.Errorf("failed to read password: %w", err)
	}
	fmt.Println() // Add a newline after the user presses enter.
	return string(bytePassword), nil
}

// accessSecretVersion fetches a secret from Google Cloud Secret Manager.
func accessSecretVersion(name string) (string, error) {
	ctx := context.Background()
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create secretmanager client: %w", err)
	}
	defer func() {
		_ = client.Close()
	}()

	req := &secretmanagerpb.AccessSecretVersionRequest{
		Name: name,
	}

	result, err := client.AccessSecretVersion(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to access secret version: %w", err)
	}

	return string(result.Payload.Data), nil
}
