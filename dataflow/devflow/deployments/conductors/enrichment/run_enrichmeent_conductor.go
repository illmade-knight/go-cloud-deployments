// cmd/deployer/main.go

package main

import (
	"context"
	_ "embed" // Required for go:embed
	"flag"
	"os"
	"time"

	"github.com/illmade-knight/go-cloud-manager/pkg/orchestration"
	"github.com/illmade-knight/go-cloud-manager/pkg/servicemanager"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

//go:embed services.yaml
var servicesYAML []byte

func main() {
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// --- 1. Define flags for command-line control ---
	projectID := flag.String("project-id", "", "GCP Project ID (required).")
	teardown := flag.Bool("teardown", false, "If true, tear down all deployed services instead of deploying.")
	flag.Parse()

	if *projectID == "" {
		logger.Fatal().Msg("Missing required flag: -project-id.")
	}

	// --- 2. Load and configure the architecture ---
	var arch servicemanager.MicroserviceArchitecture
	err := yaml.Unmarshal(servicesYAML, &arch)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to parse embedded services.yaml")
	}
	arch.ProjectID = *projectID
	logger.Info().Str("project_id", arch.ProjectID).Msg("Loaded service architecture")

	// --- 3. Hydrate architecture and prepare all service sources ---
	err = servicemanager.HydrateArchitecture(&arch, "cloud-deploy", logger.With().Str("phase", "hydration").Logger())
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to hydrate architecture")
	}

	log.Info().Msg("Generating service-specific YAML configurations...")
	serviceConfigs, err := orchestration.GenerateServiceConfigs(&arch)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to generate service configs")
	}

	// REFACTOR: This critical step writes the generated `resources.yaml` files
	// to each service's source directory so they can be embedded and tested.
	if err := orchestration.WriteServiceConfigFiles(serviceConfigs, logger); err != nil {
		log.Fatal().Err(err).Msg("Failed to write service config files")
	}

	err = orchestration.PrepareServiceDirectorSource(&arch, log.Logger)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to prepare ServiceDirector source")
	}
	log.Info().Msg("✅ All service sources prepared successfully.")

	// --- 4. Run either the deployment or teardown workflow ---
	opts := orchestration.ConductorOptions{
		CheckPrerequisites:      true,
		PreflightServiceConfigs: true, // REFACTOR: Enable the pre-build `go test` validation.
		SetupIAM:                true,
		BuildAndDeployServices:  true,
		TriggerRemoteSetup:      true,
		VerifyDataflowIAM:       true,
		SAPollTimeout:           time.Minute * 6,
		PolicyPollTimeout:       time.Minute * 6,
	}

	conductor, err := orchestration.NewConductor(ctx, &arch, log.Logger, opts)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create conductor")
	}

	// Note: The conductor.Run() method already includes a pre-flight check when the option is enabled.
	// Calling it here separately is redundant but harmless.
	preflight, cancel := context.WithTimeout(ctx, time.Minute)
	err = conductor.Preflight(preflight)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed preflight")
	}
	cancel()

	err = conductor.GenerateIAMPlan("conductors/enrichment/ice_store_iam.yaml")
	if err != nil {
		log.Info().Err(err).Msg("could not generate iam plan")
	}

	if *teardown {
		runTeardown(ctx, conductor)
	} else {
		runDeployment(ctx, conductor)
	}
}

func runDeployment(ctx context.Context, conductor *orchestration.Conductor) {
	log.Info().Msg("Starting deployment conductor...")
	err := conductor.Run(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("Conductor run failed")
	}
	log.Info().Msg("✅ Conductor successfully deployed the full architecture.")
}

func runTeardown(ctx context.Context, conductor *orchestration.Conductor) {
	log.Info().Msg("Starting teardown...")
	err := conductor.Teardown(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Conductor teardown failed")
	}
	log.Info().Msg("✅ Teardown complete.")
}
