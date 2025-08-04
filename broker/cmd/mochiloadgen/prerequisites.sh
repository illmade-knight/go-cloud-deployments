#!/bin/bash
# Performs a one-time setup of a Google Cloud project.
# This script enables all necessary APIs, creates persistent resources like
# Artifact Registry and Secret Manager, and sets all required IAM permissions.
# It is idempotent and safe to re-run.

set -e # Exit immediately if a command exits with a non-zero status.

# --- Configuration ---
# Set this to false if you want to manage the VPC Connector and NAT gateway manually to save costs
CREATE_PERSISTENT_NETWORKING=true

PROJECT_ID="gemini-power-test"
REGION="europe-west2"
USER_EMAIL=$(gcloud config get-value account)

AR_REPO="cloud-deploy"
SECRET_NAME="SERVICE_PASS"
SA_NAME="mochi-vm-sa"
VPC_CONNECTOR_NAME="test-vpc-connector"

# --- 1. Enable All Required APIs ---
echo "### Enabling all necessary APIs... ###"
APIS=(
    "compute.googleapis.com"
    "cloudbuild.googleapis.com"
    "artifactregistry.googleapis.com"
    "secretmanager.googleapis.com"
    "vpcaccess.googleapis.com"
    "iam.googleapis.com"
    "cloudresourcemanager.googleapis.com"
    "iap.googleapis.com"
    "logging.googleapis.com"
)

for API in "${APIS[@]}"; do
    echo "Enabling $API..."
    gcloud services enable "$API" --project="$PROJECT_ID"
done

# --- 2. Create Prerequisite Resources ---
echo "### Creating prerequisite resources... ###"
if ! gcloud artifacts repositories describe "$AR_REPO" --project="$PROJECT_ID" --location="$REGION" --quiet > /dev/null 2>&1; then
    echo "Creating Artifact Registry repository '$AR_REPO'..."
    gcloud artifacts repositories create "$AR_REPO" --project="$PROJECT_ID" --repository-format="docker" --location="$REGION" --description="Repo for test images"
else
    echo "Repository '$AR_REPO' already exists."
fi

if ! gcloud secrets describe "$SECRET_NAME" --project="$PROJECT_ID" --quiet > /dev/null 2>&1; then
    echo "Creating secret '$SECRET_NAME'..."
    echo -n "placeholder-password" > temp_secret.txt
    gcloud secrets create "$SECRET_NAME" --project="$PROJECT_ID" --replication-policy="automatic"
    gcloud secrets versions add "$SECRET_NAME" --project="$PROJECT_ID" --data-file="temp_secret.txt"
    rm temp_secret.txt
else
    echo "Secret '$SECRET_NAME' already exists."
fi

# --- 3. Create and Configure Service Account ---
echo "### Setting up required Service Account '$SA_NAME'... ###"
SA_EMAIL="${SA_NAME}@${PROJECT_ID}.iam.gserviceaccount.com"
if ! gcloud iam service-accounts describe "$SA_EMAIL" --project="$PROJECT_ID" --quiet > /dev/null 2>&1; then
    echo "Service Account not found, creating it now..."
    gcloud iam service-accounts create "$SA_NAME" --project="$PROJECT_ID" --display-name="Mochi GCE VM Service Account"
else
    echo "Service Account '$SA_NAME' already exists."
fi

echo "Ensuring Service Account has all required permissions..."
gcloud projects add-iam-policy-binding "$PROJECT_ID" --member="serviceAccount:$SA_EMAIL" --role="roles/secretmanager.secretAccessor" --quiet
gcloud projects add-iam-policy-binding "$PROJECT_ID" --member="serviceAccount:$SA_EMAIL" --role="roles/artifactregistry.reader" --quiet
gcloud projects add-iam-policy-binding "$PROJECT_ID" --member="serviceAccount:$SA_EMAIL" --role="roles/logging.logWriter" --quiet
echo "Permissions are set."

# --- 4. Grant IAP Tunnel User Role to Yourself ---
echo "### Granting IAP Tunnel User role to '$USER_EMAIL'... ###"
gcloud projects add-iam-policy-binding "$PROJECT_ID" --member="user:$USER_EMAIL" --role="roles/iap.tunnelResourceAccessor" --quiet

# --- 5. Create Persistent Networking Resources (Optional) ---
if [ "$CREATE_PERSISTENT_NETWORKING" = true ]; then
    echo "### Setting up persistent networking... ###"
    if ! gcloud compute networks vpc-access connectors describe "$VPC_CONNECTOR_NAME" --project="$PROJECT_ID" --region="$REGION" --quiet > /dev/null 2>&1; then
        echo "Creating VPC Connector '$VPC_CONNECTOR_NAME' (this will take several minutes)..."
        gcloud compute networks vpc-access connectors create "$VPC_CONNECTOR_NAME" --project="$PROJECT_ID" --region="$REGION" --network="default" --range="10.8.0.0/28"
    else
        echo "VPC Connector already exists."
    fi
else
    echo "### Skipping persistent networking creation as per configuration. ###"
fi

echo -e "\n✅ Project setup is complete."