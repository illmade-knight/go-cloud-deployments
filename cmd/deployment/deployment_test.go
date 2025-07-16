//go:build cloud_integration

package deployment_test

import (
	"archive/tar"
	"bytes"
	"cloud.google.com/go/pubsub"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cloud.google.com/go/cloudbuild/apiv1/v2"
	"cloud.google.com/go/cloudbuild/apiv1/v2/cloudbuildpb"
	"cloud.google.com/go/storage"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestBuildableVariableIsolator(t *testing.T) {
	// --- Setup ---
	projectID := checkGCPAuth(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	storageClient, err := storage.NewClient(ctx)
	require.NoError(t, err)
	defer storageClient.Close()

	// --- 1. Create a temporary 'toy' source directory ---
	// These files have no external or internal dependencies.
	toySource := map[string]string{
		"go.mod":              "module toy-project",
		"main.go":             `package main; import "fmt"; func main() { fmt.Println("wrong main") }`,
		"cmd/realapp/main.go": `package main; import "fmt"; func main() { fmt.Println("correct main") }`,
	}
	sourceDir, cleanupSourceDir := createTestSourceDir(t, toySource)
	defer cleanupSourceDir()

	// --- 2. Upload the toy source to GCS ---
	sourceBucketName := fmt.Sprintf("%s_cloudbuild", projectID)
	sourceObject := fmt.Sprintf("source/isolator-test-%d.tar.gz", time.Now().Unix())
	err = uploadDirToGCS(ctx, storageClient, sourceDir, sourceBucketName, sourceObject)
	require.NoError(t, err)
	t.Cleanup(func() {
		storageClient.Bucket(sourceBucketName).Object(sourceObject).Delete(context.Background())
	})

	// --- 3. Define a minimal build request to test the variable ---
	outputImage := fmt.Sprintf("us-central1-docker.pkg.dev/%s/test-images/isolator-test:%s", projectID, uuid.New().String()[:8])
	mainBuildCommand := fmt.Sprintf(`pack build %s`, outputImage)

	buildSteps := []*cloudbuildpb.BuildStep{
		{
			Name:       "gcr.io/k8s-skaffold/pack",
			Id:         "pre-buildpack",
			Entrypoint: "sh",
			Args:       []string{"-c", "chmod a+w /workspace && pack config default-builder gcr.io/buildpacks/builder:latest"},
		},
		{
			Name:       "gcr.io/k8s-skaffold/pack",
			Id:         "build",
			Entrypoint: "sh",
			// This sets ONLY the environment variable we want to test.
			Env:  []string{"GOOGLE_BUILDABLE=./cmd/realapp"},
			Args: []string{"-c", mainBuildCommand},
		},
	}

	req := &cloudbuildpb.CreateBuildRequest{
		ProjectId: projectID,
		Build: &cloudbuildpb.Build{
			Source: &cloudbuildpb.Source{
				Source: &cloudbuildpb.Source_StorageSource{
					StorageSource: &cloudbuildpb.StorageSource{
						Bucket: sourceBucketName,
						Object: sourceObject,
					},
				},
			},
			Steps: buildSteps,
		},
	}

	// --- 4. Act & Assert ---
	t.Log("Triggering isolated build to test GOOGLE_BUILDABLE...")
	buildClient, err := cloudbuild.NewClient(ctx)
	require.NoError(t, err)
	defer buildClient.Close()

	op, err := buildClient.CreateBuild(ctx, req)
	require.NoError(t, err)

	resp, err := op.Wait(ctx)
	require.NoError(t, err, "The diagnostic build operation failed")

	finalStatus := resp.GetStatus()
	t.Logf("Diagnostic build completed with final status: %s", finalStatus)

	require.Equal(t, cloudbuildpb.Build_SUCCESS, finalStatus, "Build failed, confirming GOOGLE_BUILDABLE is not being respected.")

	t.Log("✅ Diagnostic build SUCCEEDED. GOOGLE_BUILDABLE is working as expected.")
}

// checkGCPAuth is a helper that fails fast if the test is not configured to run.
func checkGCPAuth(t *testing.T) string {
	t.Helper()
	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		t.Skip("Skipping real integration test: GCP_PROJECT_ID environment variable is not set")
	}
	// A simple adminClient creation is enough to check basic auth config
	// without performing a full API call like listing resources.
	_, err := pubsub.NewClient(context.Background(), projectID)
	if err != nil {
		t.Fatalf(`
		---------------------------------------------------------------------
		GCP AUTHENTICATION FAILED!
		---------------------------------------------------------------------
		Could not create a Google Cloud adminClient. This is likely due to
		expired or missing Application Default Credentials (ADC).

		To fix this, please run 'gcloud auth application-default login'.

		Original Error: %v
		---------------------------------------------------------------------
		`, err)
	}
	return projectID
}

// TestGoogleBuildableVariable creates a minimal project with two 'main' packages
// to verify that the GOOGLE_BUILDABLE environment variable correctly selects
// the specified application for the build.
func TestGoogleBuildableVariable(t *testing.T) {
	// --- Setup ---
	projectID := checkGCPAuth(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	storageClient, err := storage.NewClient(ctx)
	require.NoError(t, err)
	defer storageClient.Close()

	// --- 1. Create a temporary 'toy' source directory ---
	toySource := map[string]string{
		"go.mod": "module toy-project",
		// This is the "wrong" main file at the root.
		"main.go": `package main; func main() { println("wrong main") }`,
		// This is the "correct" main file we want to build.
		"cmd/realapp/main.go": `package main; func main() { println("correct main") }`,
	}
	sourceDir, cleanupSourceDir := createTestSourceDir(t, toySource)
	defer cleanupSourceDir()

	// --- 2. Upload the toy source to GCS ---
	sourceBucketName := fmt.Sprintf("%s_cloudbuild", projectID)
	sourceObject := fmt.Sprintf("source/toy-buildable-test-%d.tar.gz", time.Now().Unix())
	err = uploadDirToGCS(ctx, storageClient, sourceDir, sourceBucketName, sourceObject)
	require.NoError(t, err)
	t.Cleanup(func() {
		storageClient.Bucket(sourceBucketName).Object(sourceObject).Delete(context.Background())
	})

	// --- 3. Define a minimal build request to test the variable ---
	// We don't care about the output image, only that the build SUCCEEDS.
	outputImage := fmt.Sprintf("us-central1-docker.pkg.dev/%s/test-images/toy-test:%s", projectID, uuid.New().String()[:8])
	mainBuildCommand := fmt.Sprintf(`pack build %s --path ./cmd/realapp`, outputImage)

	buildSteps := []*cloudbuildpb.BuildStep{
		{
			Name:       "gcr.io/k8s-skaffold/pack",
			Id:         "pre-buildpack",
			Entrypoint: "/bin/sh",
			Args:       []string{"-c", "chmod a+w /workspace && pack config default-builder gcr.io/buildpacks/builder:latest"},
		},
		{
			Name:       "gcr.io/k8s-skaffold/pack",
			Id:         "build",
			Entrypoint: "/bin/sh",
			// No 'Env' variable is needed when using the --path flag.
			Args: []string{"-c", mainBuildCommand},
		},
	}

	req := &cloudbuildpb.CreateBuildRequest{
		ProjectId: projectID,
		Build: &cloudbuildpb.Build{
			Source: &cloudbuildpb.Source{
				Source: &cloudbuildpb.Source_StorageSource{
					StorageSource: &cloudbuildpb.StorageSource{
						Bucket: sourceBucketName,
						Object: sourceObject,
					},
				},
			},
			Steps: buildSteps,
		},
	}

	// --- 4. Act & Assert ---
	t.Log("Triggering minimal build to test GOOGLE_BUILDABLE...")
	buildClient, err := cloudbuild.NewClient(ctx)
	require.NoError(t, err)
	defer buildClient.Close()

	op, err := buildClient.CreateBuild(ctx, req)
	require.NoError(t, err)

	resp, err := op.Wait(ctx)
	require.NoError(t, err, "The diagnostic build operation failed")

	finalStatus := resp.GetStatus()
	t.Logf("Diagnostic build completed with final status: %s", finalStatus)

	// The only thing that matters is if the build succeeds.
	require.Equal(t, cloudbuildpb.Build_SUCCESS, finalStatus, "Build failed, indicating GOOGLE_BUILDABLE was not respected.")

	t.Log("✅ Diagnostic build SUCCEEDED. GOOGLE_BUILDABLE is working as expected.")
}

func createTestSourceDir(t *testing.T, files map[string]string) (string, func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "source")
	require.NoError(t, err)

	for name, content := range files {
		// Get the full path for the new file.
		p := filepath.Join(tmpDir, name)

		// Get the directory part of the path.
		dir := filepath.Dir(p)

		// Create the parent directory structure if it doesn't exist.
		err = os.MkdirAll(dir, 0755)
		require.NoError(t, err, "Failed to create source subdirectories")

		// Now, write the file.
		err = os.WriteFile(p, []byte(content), 0644)
		require.NoError(t, err, "Failed to write source file")
	}

	return tmpDir, func() { os.RemoveAll(tmpDir) }
}

// Helper function to upload a directory to GCS as a tar.gz archive.
func uploadDirToGCS(ctx context.Context, client *storage.Client, sourceDir, bucket, objectName string) error {
	buf := new(bytes.Buffer)
	gzipWriter := gzip.NewWriter(buf)
	tarWriter := tar.NewWriter(gzipWriter)

	err := filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		header, err := tar.FileInfoHeader(info, info.Name())
		if err != nil {
			return err
		}

		// Get the relative path of the file.
		header.Name, err = filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}

		// CORRECTED: Ensure all path separators are forward slashes for tar compatibility.
		header.Name = filepath.ToSlash(header.Name)

		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(tarWriter, file)
		return err
	})

	if err != nil {
		return fmt.Errorf("failed to walk source directory '%s': %w", sourceDir, err)
	}
	tarWriter.Close()
	gzipWriter.Close()

	w := client.Bucket(bucket).Object(objectName).NewWriter(ctx)
	if _, err = io.Copy(w, buf); err != nil {
		w.Close() // Close writer on error
		return fmt.Errorf("failed to copy source to GCS: %w", err)
	}
	return w.Close()
}
