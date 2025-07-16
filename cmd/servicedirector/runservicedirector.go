package main

import (
	"context"
	"github.com/illmade-knight/go-cloud-manager/microservice/servicedirector"
	"github.com/illmade-knight/go-cloud-manager/pkg/servicemanager"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	// Use a console writer for pretty, human-readable logs.
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	// Load the Director's own configuration. This will read flags like
	// --services-def-path to find the services.yaml file.
	cfg, err := servicedirector.NewConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load servicedirector config")
	}

	// --- THIS IS THE NEW LOGIC ---
	// Check for a deployment environment variable.
	deploymentEnv := os.Getenv("DEPLOYMENT_ENV")
	if deploymentEnv != "" {
		// If running in a deployed environment (e.g., "prod", "dev"), use the fixed container path.
		log.Info().Str("env", deploymentEnv).Msg("Deployment environment detected.")
		// The build process places the file at this path inside the container.
		cfg.ServicesDefPath = "cmd/servicedirector/services.yaml"
	} else {
		// For local development, if the default services.yaml is not found,
		// try a path relative to the project root. This makes `go run` work
		// from the root directory without needing to pass a flag.
		if _, err := os.Stat(cfg.ServicesDefPath); os.IsNotExist(err) {
			log.Warn().
				Str("path", cfg.ServicesDefPath).
				Msg("Could not find services.yaml at default path. Trying relative path for local dev.")
			devPath := "cmd/servicedirector/services.yaml" // Correct relative path from project root
			if _, err2 := os.Stat(devPath); err2 == nil {
				log.Info().Str("path", devPath).Msg("Found services.yaml at dev path.")
				cfg.ServicesDefPath = devPath
			} else {
				log.Fatal().Err(err).Str("checked_paths", cfg.ServicesDefPath+", "+devPath).Msg("services.yaml definition file not found")
			}
		}
	}

	//TODO we need to get these from config
	dataflowPaths := make([]string, 2)
	outputFilePath := ""

	// Create a loader that reads definitions from the specified YAML file.
	loader, err := servicemanager.NewYAMLArchitectureIO(cfg.ServicesDefPath, dataflowPaths, outputFilePath)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load service director architecture")
	}
	log.Info().Str("yaml", cfg.ServicesDefPath).Msg("Loading services.yaml")

	// Create the Director instance.
	schemaRegistry := map[string]interface{}{}
	director, err := servicedirector.NewServiceDirector(context.Background(), cfg, loader, schemaRegistry, log.Logger)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create Director")
	}

	// Start the service. This is non-blocking.
	if err := director.Start(); err != nil {
		log.Fatal().Err(err).Msg("Failed to start Director")
	}
	log.Info().Str("port", director.GetHTTPPort()).Msg("Director is running")

	// Wait for a shutdown signal.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info().Msg("Shutdown signal received, stopping Director...")

	// Gracefully shut down the service.
	director.Shutdown()
	log.Info().Msg("Director stopped.")
}
