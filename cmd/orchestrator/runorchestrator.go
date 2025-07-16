package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/illmade-knight/go-cloud-manager/pkg/deployment"
	"github.com/illmade-knight/go-cloud-manager/pkg/servicemanager"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"google.golang.org/api/option"
	"google.golang.org/api/run/v2"
)

func main() {
	// Use a console writer for pretty, human-readable logs.
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	logger := log.Logger

	// --- 1. Configure the Orchestrator via Flags ---
	projectID := flag.String("project-id", "", "The Google Cloud Project ID (required).")
	sourcePath := flag.String("source-path", ".", "Path to the source code to deploy.")
	region := flag.String("region", "us-central1", "The GCP region for the deployment.")
	imageRepo := flag.String("image-repo", "test-images", "The Artifact Registry repository name.")
	serviceName := flag.String("service-name", "service-director", "The name for the deployed Cloud Run service.")
	runtimeSA := flag.String("runtime-sa-name", "service-director-sa", "The name of the service account for the deployed service to run as.")

	deploymentEnv := flag.String("deployment-env", "test", "The deployment environment (e.g., dev, test, prod).")
	modulePath := flag.String("module-path", "", "The relative path to the main package to build (e.g., ./cmd/servicedirector).")

	flag.Parse()

	if *projectID == "" {
		logger.Fatal().Msg("Error: -project-id flag is required.")
	}

	logger.Info().Str("service", *serviceName).Msg("Starting deployment orchestration...")
	ctx := context.Background()

	// --- 2. Instantiate the Deployer ---
	sourceBucketName := fmt.Sprintf("%s_cloudbuild", *projectID)
	deployer, err := deployment.NewCloudBuildDeployer(ctx, *projectID, *region, sourceBucketName, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to create CloudBuildDeployer")
	}

	// --- 3. Ensure Runtime Service Account Exists ---
	logger.Info().Str("sa_name", *runtimeSA).Msg("Ensuring runtime service account exists...")
	iamClient, err := deployment.NewGoogleIAMClient(ctx, *projectID)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to create IAM client")
	}
	runtimeSAEmail, err := iamClient.EnsureServiceAccountExists(ctx, *runtimeSA)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to ensure service account exists")
	}
	logger.Info().Str("sa_email", runtimeSAEmail).Msg("Runtime service account is ready.")

	// --- 4. Define the Deployment Specification ---
	imageTag := fmt.Sprintf("%s-docker.pkg.dev/%s/%s/%s:%s", *region, *projectID, *imageRepo, *serviceName, uuid.New().String()[:8])

	spec := servicemanager.DeploymentSpec{
		SourcePath:          *sourcePath,
		Image:               imageTag,
		CPU:                 "1",
		Memory:              "512Mi",
		BuildableModulePath: *modulePath, // Set the new field from the flag
		BuildEnvironmentVars: map[string]string{
			"GOOGLE_BUILDABLE": *modulePath,
		},
		EnvironmentVars: map[string]string{
			"DEPLOYMENT_ENV": *deploymentEnv,
		},
	}

	// --- 5. Execute the Full Deployment Workflow ---
	// This uses your existing, single Deploy function.
	serviceURL, err := deployer.Deploy(ctx, *serviceName, "", runtimeSAEmail, spec)
	if err != nil {
		logger.Fatal().Err(err).Msg("Deployment failed")
	}
	logger.Info().Str("url", serviceURL).Msg("Deployment API call succeeded. Verifying revision readiness...")

	// --- 6. Wait for Service to Become Ready ---
	// This polling loop ensures the orchestrator doesn't exit until the service is healthy.
	regionalEndpoint := fmt.Sprintf("%s-run.googleapis.com:443", *region)
	runService, err := run.NewService(ctx, option.WithEndpoint(regionalEndpoint))
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to create regional Run client for health check")
	}

	fullServiceName := fmt.Sprintf("projects/%s/locations/%s/services/%s", *projectID, *region, *serviceName)
	deadline := time.Now().Add(3 * time.Minute)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-time.After(time.Until(deadline)):
			logger.Fatal().Msg("Timeout: Cloud Run service never became ready.")
		case <-ticker.C:
			// Get the service to find the latest revision name
			svc, err := runService.Projects.Locations.Services.Get(fullServiceName).Do()
			if err != nil {
				logger.Warn().Err(err).Msg("Polling for service status failed, retrying...")
				continue
			}
			if svc.LatestCreatedRevision == "" {
				logger.Info().Msg("Service exists, but no revision has been created yet, retrying...")
				continue
			}

			// Get the revision object directly
			rev, err := runService.Projects.Locations.Services.Revisions.Get(svc.LatestCreatedRevision).Do()
			if err != nil {
				logger.Warn().Err(err).Msg("Polling for revision status failed, retrying...")
				continue
			}

			// Check the 'Ready' condition on the revision
			isReady := false
			for _, cond := range rev.Conditions {
				if cond.Type == "Ready" {
					if cond.State == "CONDITION_SUCCEEDED" {
						isReady = true
					}
					break
				}
			}

			if isReady {
				logger.Info().Msg("✅ Revision is Ready. Orchestration successful!")
				return // Exit successfully
			}
			logger.Info().Msg("Revision not ready yet, polling again...")
		}
	}
}
