package main

import (
	"context"
	_ "embed"
	"github.com/illmade-knight/go-cloud-manager/microservice/servicedirector"
	"github.com/illmade-knight/go-cloud-manager/pkg/servicemanager"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
	"os"
	"os/signal"
	"syscall"
)

//go:embed services.yaml
var servicesYAML []byte

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	// 1. Load Application Configuration
	cfg, err := servicedirector.NewConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load servicedirector config")
	}
	ctx := context.Background()

	// 3. Load and Prepare the Architecture Definition
	arch := &servicemanager.MicroserviceArchitecture{}
	if err := yaml.Unmarshal(servicesYAML, arch); err != nil {
		log.Fatal().Err(err).Msg("Failed to parse embedded services.yaml")
	}
	// Override the project ID from the environment, as this is environment-specific.
	projectIDFromEnv := os.Getenv("PROJECT_ID")
	// OK this is a bit of a mess we need projectID truth source
	if projectIDFromEnv != "" {
		cfg.ProjectID = projectIDFromEnv
		arch.ProjectID = projectIDFromEnv
	}
	log.Info().Str("project-id", arch.ProjectID).Msg("Loaded service architecture from embedded services.yaml")

	// This uses the production constructor
	director, err := servicedirector.NewServiceDirector(ctx, cfg, arch, log.Logger)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create Director")
	}

	// 5. Start the Service and Wait for Shutdown
	if err := director.Start(); err != nil {
		log.Fatal().Err(err).Msg("Failed to start Director")
	}
	log.Info().Str("port", director.GetHTTPPort()).Msg("Director is running")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info().Msg("Shutdown signal received, stopping Director...")

	director.Shutdown(ctx)
	log.Info().Msg("Director stopped.")
}
