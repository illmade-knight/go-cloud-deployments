package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"google.golang.org/api/serviceusage/v1"
)

func main() {
	// Define flags to make the tool configurable
	projectID := flag.String("project-id", "", "The Google Cloud Project ID (required).")
	flag.Parse()

	if *projectID == "" {
		log.Fatal("Error: -project-id flag is required.")
		os.Exit(1)
	}

	log.Println("Starting full IAM and API setup for Cloud Run...")
	ctx := context.Background()

	// --- Step 1: Ensure the Cloud Run API is enabled ---
	log.Println("Checking and enabling Cloud Run API (run.googleapis.com)...")
	err := enableService(ctx, *projectID, "run.googleapis.com")
	if err != nil {
		log.Fatalf("Failed to enable Cloud Run API: %v", err)
	}
	log.Println("Cloud Run API is enabled. Waiting 30 seconds for service agent to be created...")
	time.Sleep(30 * time.Second) // Give Google time to provision the service agent
}

// enableService enables a given service on a project and waits for the operation to complete.
func enableService(ctx context.Context, projectID, serviceName string) error {
	serviceUsage, err := serviceusage.NewService(ctx)
	if err != nil {
		return fmt.Errorf("serviceusage.NewService: %w", err)
	}

	// The resource name for a service.
	name := fmt.Sprintf("projects/%s/services/%s", projectID, serviceName)

	// Check if the service is already enabled.
	getReq, err := serviceUsage.Services.Get(name).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to get service status: %w", err)
	}
	if getReq.State == "ENABLED" {
		log.Printf("Service '%s' is already enabled.", serviceName)
		return nil
	}

	// If not enabled, enable it.
	req := &serviceusage.EnableServiceRequest{}
	op, err := serviceUsage.Services.Enable(name, req).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to enable service: %w", err)
	}

	// Wait for the operation to complete.
	for !op.Done {
		log.Println("Waiting for API enablement to complete...")
		time.Sleep(2 * time.Second)
		op, err = serviceUsage.Operations.Get(op.Name).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("failed to poll operation status: %w", err)
		}
	}

	if op.Error != nil {
		return fmt.Errorf("operation to enable API failed with error: %v", op.Error)
	}

	log.Printf("Successfully enabled service '%s'", serviceName)
	return nil
}
