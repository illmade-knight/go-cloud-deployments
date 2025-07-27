// cmd/deployer/main.go

package main

import (
	"context"
	_ "embed" // Required for go:embed
	"flag"
	"fmt"
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
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// --- 1. Define flags for command-line control ---
	projectID := flag.String("project-id", "", "GCP Project ID (required)")
	teardown := flag.Bool("teardown", false, "If true, tear down all deployed services instead of deploying")

	// Add new flags for each step of the conductor's run, defaulting to true.
	runSetupIAM := flag.Bool("run-setup-iam", true, "Execute Step 1: Set up IAM for the ServiceDirector.")
	runDeployDirector := flag.Bool("run-deploy-director", true, "Execute Step 2: Deploy the ServiceDirector service.")
	runSetupResources := flag.Bool("run-setup-resources", true, "Execute Step 3: Trigger creation of dataflow resources.")
	runApplyIAM := flag.Bool("run-apply-iam", true, "Execute Step 4: Apply IAM policies for dataflow services.")
	runDeployServices := flag.Bool("run-deploy-services", true, "Execute Step 5: Deploy all dataflow microservices.")
	directorURLOverride := flag.String("director-url", "", "Override for the ServiceDirector URL if its deployment is skipped.")

	flag.Parse()

	if *projectID == "" {
		log.Fatal().Msg("Missing required flag: -project-id")
	}

	// --- 2. Load and configure the architecture ---
	var arch servicemanager.MicroserviceArchitecture
	if err := yaml.Unmarshal(servicesYAML, &arch); err != nil {
		log.Fatal().Err(err).Msg("Failed to parse embedded services.yaml")
	}
	arch.ProjectID = *projectID
	log.Info().Str("project_id", arch.ProjectID).Msg("Loaded service architecture")

	if err := servicemanager.HydrateArchitecture(&arch, "cloud-deploy", ""); err != nil {
		log.Fatal().Err(err).Msg("Failed to hydrate architecture")
	}
	log.Debug().Str("struct", fmt.Sprintf("%#v", arch)).Msg("Loaded service architecture")
	log.Info().Msg("✅ Architecture hydrated successfully.")

	// --- 3. Run either the deployment or teardown workflow ---
	if *teardown {
		runTeardown(ctx, &arch, log.Logger)
	} else {
		// Populate the options from the command-line flags.
		opts := orchestration.ConductorOptions{
			SetupServiceDirectorIAM: *runSetupIAM,
			DeployServiceDirector:   *runDeployDirector,
			SetupDataflowResources:  *runSetupResources,
			ApplyDataflowIAM:        *runApplyIAM,
			DeployDataflowServices:  *runDeployServices,
			DirectorURLOverride:     *directorURLOverride,
		}
		runDeployment(ctx, &arch, log.Logger, opts)
	}
}

func runDeployment(ctx context.Context, arch *servicemanager.MicroserviceArchitecture, logger zerolog.Logger, opts orchestration.ConductorOptions) {
	log.Info().Msg("Starting deployment conductor...")

	conductor, err := orchestration.NewConductor(ctx, arch, logger, opts)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create conductor")
	}

	// This single call orchestrates everything based on the provided options.
	if err := conductor.Run(ctx); err != nil {
		log.Fatal().Err(err).Msg("Conductor run failed")
	}

	log.Info().Msg("✅ Conductor successfully deployed the full architecture.")
}

func runTeardown(ctx context.Context, arch *servicemanager.MicroserviceArchitecture, logger zerolog.Logger) {
	log.Info().Msg("Starting teardown...")

	// To tear down the deployed services, we need access to the Orchestrator's
	// specific teardown helpers, just like in the tests.
	orch, err := orchestration.NewOrchestrator(ctx, arch, logger)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create orchestrator for teardown")
	}

	// Tear down application services first.
	for dfName := range arch.Dataflows {
		if err := orch.TeardownDataflowServices(ctx, dfName); err != nil {
			log.Error().Err(err).Str("dataflow", dfName).Msg("Failed to tear down dataflow services")
		}
	}

	// Then tear down the ServiceDirector itself.
	if err := orch.TeardownCloudRunService(ctx, arch.ServiceManagerSpec.Name); err != nil {
		log.Error().Err(err).Msg("Failed to tear down ServiceDirector service")
	}

	// Finally, tear down the conductor's own framework resources (IAM, topics).
	// Create a conductor with default options just for teardown.
	conductor, _ := orchestration.NewConductor(ctx, arch, logger, orchestration.ConductorOptions{})
	if err := conductor.Teardown(ctx); err != nil {
		log.Error().Err(err).Msg("Conductor framework teardown failed")
	}

	log.Info().Msg("✅ Teardown complete.")
}
