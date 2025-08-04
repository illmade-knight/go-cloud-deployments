// cmd/deployer/main.go

package main

import (
	"context"
	_ "embed" // Required for go:embed
	"flag"
	"fmt"
	"os"
	"path/filepath"
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
	projectID := flag.String("project-id", "", "GCP Project ID (required).")
	teardown := flag.Bool("teardown", false, "If true, tear down all deployed services instead of deploying.")

	runSetupIAM := flag.Bool("run-setup-iam", true, "Step 1: Set up IAM for the ServiceDirector.")
	runDeployDirector := flag.Bool("run-deploy-director", true, "Step 2: Deploy the ServiceDirector service.")
	runSetupResources := flag.Bool("run-setup-resources", true, "Step 3: Trigger creation of dataflow resources.")
	runApplyIAM := flag.Bool("run-apply-iam", true, "Step 4: Apply IAM policies for dataflow services.")
	runDeployServices := flag.Bool("run-deploy-services", true, "Step 5: Deploy all dataflow microservices.")
	directorURLOverride := flag.String("director-url", "", "URL of an existing ServiceDirector. Required if -run-deploy-director=false.")

	flag.Usage = func() {
		_, _ = fmt.Fprintf(os.Stderr, "Conductor - A tool for deploying a full microservice architecture.\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "This tool reads a services.yaml file, hydrates it with project-specific details,\n")
		_, _ = fmt.Fprintf(os.Stderr, "and orchestrates the deployment of IAM, resources, and services in a specific order.\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "Usage:\n")
		_, _ = fmt.Fprintf(os.Stderr, "  conductor [options]\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if *projectID == "" {
		log.Fatal().Msg("Missing required flag: -project-id. Use -h or -help for more information.")
	}

	// --- 2. Load and configure the architecture ---
	var arch servicemanager.MicroserviceArchitecture
	err := yaml.Unmarshal(servicesYAML, &arch)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to parse embedded services.yaml")
	}
	arch.ProjectID = *projectID
	log.Info().Str("project_id", arch.ProjectID).Msg("Loaded service architecture")

	err = servicemanager.HydrateArchitecture(&arch, "cloud-deploy", "", log.Logger)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to hydrate architecture")
	}
	log.Info().Msg("✅ Architecture hydrated successfully.")

	// --- 3. Copy YAML to Director's source for deployment ---
	err = copyYAMLToDirectorSource(&arch)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to copy services.yaml to director source")
	}

	// --- 4. Run either the deployment or teardown workflow ---
	opts := orchestration.ConductorOptions{
		SetupServiceDirectorIAM: *runSetupIAM,
		DeployServiceDirector:   *runDeployDirector,
		SetupDataflowResources:  *runSetupResources,
		ApplyDataflowIAM:        *runApplyIAM,
		DeployDataflowServices:  *runDeployServices,
		DirectorURLOverride:     *directorURLOverride,
	}

	conductor, err := orchestration.NewConductor(ctx, &arch, log.Logger, opts)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create conductor")
	}

	if *teardown {
		runTeardown(ctx, conductor)
	} else {
		runDeployment(ctx, conductor)
	}
}

// copyYAMLToDirectorSource writes the embedded services.yaml content to the
// source directory of the ServiceDirector service. This ensures the file is
// included in the source archive that gets uploaded for the build.
func copyYAMLToDirectorSource(arch *servicemanager.MicroserviceArchitecture) error {
	if arch.ServiceManagerSpec.Deployment == nil || arch.ServiceManagerSpec.Deployment.SourcePath == "" {
		return fmt.Errorf("ServiceManagerSpec.Deployment.SourcePath is not defined in the architecture")
	}

	//it's the BuildableModulePath we want not SourcePath as the source path is path of all the code i.e where the mod file is
	directorSourcePath := arch.ServiceManagerSpec.Deployment.BuildableModulePath
	destinationPath := filepath.Join(directorSourcePath, "services.yaml")

	log.Info().Str("destination", destinationPath).Msg("Copying embedded services.yaml to ServiceDirector source directory...")

	// Write the embedded YAML content to the destination file.
	// 0644 provides standard read/write permissions for the owner and read for others.
	err := os.WriteFile(destinationPath, servicesYAML, 0644)
	if err != nil {
		return fmt.Errorf("failed to write services.yaml to %s: %w", destinationPath, err)
	}
	log.Info().Msg("✅ Successfully copied services.yaml.")
	return nil
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
		// Log the error but don't exit fatally, to allow for partial teardowns.
		log.Error().Err(err).Msg("Conductor teardown failed")
	}

	log.Info().Msg("✅ Teardown complete.")
}
