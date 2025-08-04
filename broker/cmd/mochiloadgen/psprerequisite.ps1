#Requires -Version 5.1
<#
.SYNOPSIS
    Performs a one-time setup of a Google Cloud project to prepare it for
    the Mochi MQTT test deployment.
.DESCRIPTION
    This script checks for and creates all necessary APIs, IAM permissions,
    and base resources. It is idempotent and safe to re-run.
#>

# --- Configuration ---
$ErrorActionPreference = 'Stop'

$projectId = "gemini-power-test"
$region = "europe-west2"
$userEmail = $(gcloud config get-value account) # Automatically gets your logged-in email

$arRepo = "cloud-deploy"
$secretName = "SERVICE_PASS"
$serviceAccountName = "mochi-vm-sa"
$vpcConnectorName = "test-vpc-connector"
$cloudRouterName = "test-nat-router"
$cloudNatName = "test-nat-gateway"

# --- 1. Enable All Required APIs ---
Write-Host "### Enabling all necessary APIs... ###" -ForegroundColor Yellow
$apis = @(
    "compute.googleapis.com",
    "cloudbuild.googleapis.com",
    "artifactregistry.googleapis.com",
    "secretmanager.googleapis.com",
    "vpcaccess.googleapis.com",
    "iam.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "iap.googleapis.com",
    "logging.googleapis.com" # Added for clarity
)

foreach ($api in $apis) {
    Write-Host "Enabling $api..."
    gcloud services enable $api --project=$projectId
}

# --- 2. Create Prerequisite Resources (if they don't exist) ---
Write-Host "### Creating prerequisite resources... ###" -ForegroundColor Yellow

# Create Artifact Registry Repository
Write-Host "Checking for Artifact Registry repository: $arRepo..."
$repo = gcloud artifacts repositories describe $arRepo --project=$projectId --location=$region --format="value(name)" --quiet -ErrorAction SilentlyContinue
if (-not $repo) {
    Write-Host "Creating Artifact Registry repository '$arRepo'..."
    gcloud artifacts repositories create $arRepo --project=$projectId --repository-format="docker" --location=$region --description="Repo for test images"
} else { Write-Host "Repository '$arRepo' already exists." }

# Create Secret Manager Secret
Write-Host "Checking for Secret Manager secret: $secretName..."
$secret = gcloud secrets describe $secretName --project=$projectId --format="value(name)" --quiet -ErrorAction SilentlyContinue
if (-not $secret) {
    Write-Host "Creating secret '$secretName'..."
    Set-Content -Path "temp_secret.txt" -Value "placeholder-password"
    gcloud secrets create $secretName --project=$projectId --replication-policy="automatic"
    gcloud secrets versions add $secretName --project=$projectId --data-file="temp_secret.txt"
    Remove-Item "temp_secret.txt"
} else { Write-Host "Secret '$secretName' already exists." }

# --- 3. Create and Configure Service Account ---
Write-Host "### Setting up required Service Account '$($serviceAccountName)'... ###" -ForegroundColor Yellow
$sa_email = "$($serviceAccountName)@$($projectId).iam.gserviceaccount.com"
$sa = gcloud iam service-accounts list --project=$projectId --filter="email($sa_email)" --format="value(email)" --quiet
if (-not $sa) {
    Write-Host "Service Account not found, creating it now: $sa_email"
    gcloud iam service-accounts create $serviceAccountName --project=$projectId --display-name="Mochi GCE VM Service Account"
} else {
    Write-Host "Service Account '$($serviceAccountName)' already exists."
}

# Grant all required permissions to ensure they are always present
Write-Host "Ensuring Service Account has all required permissions..."
gcloud projects add-iam-policy-binding $projectId --member="serviceAccount:$sa_email" --role="roles/secretmanager.secretAccessor" --quiet
gcloud projects add-iam-policy-binding $projectId --member="serviceAccount:$sa_email" --role="roles/artifactregistry.reader" --quiet
# --- THIS LINE HAS BEEN ADDED ---
gcloud projects add-iam-policy-binding $projectId --member="serviceAccount:$sa_email" --role="roles/logging.logWriter" --quiet
Write-Host "All permissions set."

# --- 4. Grant IAP Tunnel User Role to Yourself ---
Write-Host "### Granting IAP Tunnel User role to '$($userEmail)'... ###" -ForegroundColor Yellow
gcloud projects add-iam-policy-binding $projectId --member="user:$userEmail" --role="roles/iap.tunnelResourceAccessor"

# --- 5. Create Persistent Networking Resources ---
Write-Host "### Setting up persistent networking... ###" -ForegroundColor Yellow
# (Logic for VPC Connector, Router, and NAT remains the same)
$connector = gcloud compute networks vpc-access connectors describe $vpcConnectorName --project=$projectId --region=$region --format="value(name)" --quiet
if (-not $connector) {
    Write-Host "Creating VPC Connector '$vpcConnectorName'..."
    gcloud compute networks vpc-access connectors create $vpcConnectorName --project=$projectId --region=$region --network="default" --range="10.8.0.0/28"
} else { Write-Host "VPC Connector already exists." }

$router = gcloud compute routers describe $cloudRouterName --project=$projectId --region=$region --format="value(name)" --quiet
if (-not $router) {
    Write-Host "Creating Cloud Router '$cloudRouterName'..."
    gcloud compute routers create $cloudRouterName --project=$projectId --region=$region --network=default --asn=64512
} else { Write-Host "Cloud Router already exists." }

$nat = gcloud compute routers nats describe $cloudNatName --router=$cloudRouterName --project=$projectId --region=$region --format="value(name)" --quiet
if (-not $nat) {
    Write-Host "Creating Cloud NAT Gateway '$cloudNatName'..."
    gcloud compute routers nats create $cloudNatName --router=$cloudRouterName --project=$projectId --region=$region --auto-allocate-nat-external-ips --nat-all-subnet-ip-ranges
} else { Write-Host "Cloud NAT Gateway already exists." }

Write-Host "`n✅ Project setup is complete." -ForegroundColor Green