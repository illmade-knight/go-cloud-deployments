#Requires -Version 5.1
<#
.SYNOPSIS
    Performs a one-time setup of a Google Cloud project.
.DESCRIPTION
    This script enables all necessary APIs, creates persistent resources like
    Artifact Registry and Secret Manager, and sets all required IAM permissions.
    It is idempotent and safe to re-run.
#>

# --- Configuration ---
$ErrorActionPreference = 'Stop'

# Set this to $false if you want to manage the VPC Connector and NAT gateway manually to save costs
$createPersistentNetworking = $true

$projectId = "gemini-power-test"
$region = "europe-west2"
$userEmail = $(gcloud config get-value account)

$arRepo = "cloud-deploy"
$secretName = "SERVICE_PASS"
$serviceAccountName = "mochi-vm-sa"
$vpcConnectorName = "test-vpc-connector"
$cloudRouterName = "test-nat-router"
$cloudNatName = "test-nat-gateway"

# --- 1. Enable All Required APIs ---
Write-Host "### Enabling all necessary APIs... ###" -ForegroundColor Yellow
$apis = @( "compute.googleapis.com", "cloudbuild.googleapis.com", "artifactregistry.googleapis.com", "secretmanager.googleapis.com", "vpcaccess.googleapis.com", "iam.googleapis.com", "cloudresourcemanager.googleapis.com", "iap.googleapis.com", "logging.googleapis.com" )
foreach ($api in $apis) {
    Write-Host "Enabling $api..."
    gcloud services enable $api --project=$projectId
}

# --- 2. Create Prerequisite Resources ---
Write-Host "### Creating prerequisite resources... ###" -ForegroundColor Yellow
$repo = gcloud artifacts repositories describe $arRepo --project=$projectId --location=$region --format="value(name)" --quiet -ErrorAction SilentlyContinue
if (-not $repo) {
    Write-Host "Creating Artifact Registry repository '$arRepo'..."
    gcloud artifacts repositories create $arRepo --project=$projectId --repository-format="docker" --location=$region --description="Repo for test images"
} else { Write-Host "Repository '$arRepo' already exists." }

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
    Write-Host "Service Account not found, creating it now..."
    gcloud iam service-accounts create $serviceAccountName --project=$projectId --display-name="Mochi GCE VM Service Account"
} else { Write-Host "Service Account already exists." }

Write-Host "Ensuring Service Account has all required permissions..."
gcloud projects add-iam-policy-binding $projectId --member="serviceAccount:$sa_email" --role="roles/secretmanager.secretAccessor" --quiet
gcloud projects add-iam-policy-binding $projectId --member="serviceAccount:$sa_email" --role="roles/artifactregistry.reader" --quiet
gcloud projects add-iam-policy-binding $projectId --member="serviceAccount:$sa_email" --role="roles/logging.logWriter" --quiet
Write-Host "Permissions are set."

# --- 4. Grant IAP Tunnel User Role to Yourself ---
Write-Host "### Granting IAP Tunnel User role to '$($userEmail)'... ###" -ForegroundColor Yellow
gcloud projects add-iam-policy-binding $projectId --member="user:$userEmail" --role="roles/iap.tunnelResourceAccessor" --quiet

# --- 5. Create Persistent Networking Resources (Optional) ---
if ($createPersistentNetworking) {
    Write-Host "### Setting up persistent networking... ###" -ForegroundColor Yellow
    $connector = gcloud compute networks vpc-access connectors describe $vpcConnectorName --project=$projectId --region=$region --format="value(name)" --quiet -ErrorAction SilentlyContinue
    if (-not $connector) {
        Write-Host "Creating VPC Connector '$vpcConnectorName' (this will take several minutes)..."
        gcloud compute networks vpc-access connectors create $vpcConnectorName --project=$projectId --region=$region --network="default" --range="10.8.0.0/28"
    } else { Write-Host "VPC Connector already exists." }
} else {
    Write-Host "### Skipping persistent networking creation as per configuration. ###" -ForegroundColor Yellow
}

Write-Host "`n✅ Project setup is complete." -ForegroundColor Green