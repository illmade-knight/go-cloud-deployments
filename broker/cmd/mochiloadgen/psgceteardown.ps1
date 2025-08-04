#Requires -Version 5.1
<#
.SYNOPSIS
    Performs a COMPLETE teardown of the Mochi test environment.

.DESCRIPTION
    This script deletes ALL resources created for the test environment,
    including shared resources that incur costs while idle. It is designed to be
    run after testing is complete to stop all related charges.

    Deletes the following:
    1. GCE VM Instance (found by network tag)
    2. Firewall Rule (found by network tag)
    3. Cloud NAT Gateway
    4. Cloud Router
    5. Serverless VPC Access Connector
#>

# --- Configuration (ensure this matches your deploy script) ---
# Set the error action preference so the script continues if a resource is already deleted.
$ErrorActionPreference = 'SilentlyContinue'

$env:PROJECT_ID = "gemini-power-test"
$env:REGION = "europe-west2"
$env:ZONE = "$($env:REGION)-a"
$env:NETWORK_TAG_MOCHI = "mochi-test-server"
$env:CLOUD_ROUTER_NAME = "test-nat-router"
$env:CLOUD_NAT_NAME = "test-nat-gateway"
$env:VPC_CONNECTOR_NAME = "test-vpc-connector"

# --- 1. Find and Delete GCE VM Instance ---
Write-Host "### 1. Searching for GCE VM with tag: $($env:NETWORK_TAG_MOCHI)... ###"
$vmInstance = gcloud compute instances list --project=$env:PROJECT_ID --filter="tags.items=$($env:NETWORK_TAG_MOCHI)" --format="value(name)" --quiet

if ($vmInstance) {
    Write-Host "Found VM: $vmInstance. Deleting..."
    gcloud compute instances delete $vmInstance --project=$env:PROJECT_ID --zone=$env:ZONE --quiet
    Write-Host "✅ GCE VM '$vmInstance' deleted."
} else {
    Write-Host "INFO: No GCE VM with tag '$($env:NETWORK_TAG_MOCHI)' found."
}


# --- 2. Find and Delete Firewall Rule ---
Write-Host "### 2. Searching for Firewall Rule with target tag: $($env:NETWORK_TAG_MOCHI)... ###"
$firewallRules = gcloud compute firewall-rules list --project=$env:PROJECT_ID --filter="targetTags.items=$($env:NETWORK_TAG_MOCHI)" --format="value(name)" --quiet

if ($firewallRules) {
    foreach ($rule in $firewallRules) {
        Write-Host "Found Firewall Rule: $rule. Deleting..."
        gcloud compute firewall-rules delete $rule --project=$env:PROJECT_ID --quiet
        Write-Host "✅ Firewall Rule '$rule' deleted."
    }
} else {
    Write-Host "INFO: No Firewall Rule with target tag '$($env:NETWORK_TAG_MOCHI)' found."
}


# --- 3. Delete Cloud NAT and Router ---
Write-Host "### 3. Deleting Cloud NAT Gateway and associated Router... ###"
# IMPORTANT: The NAT Gateway must be deleted before the Router it's attached to.

# Check for and delete the NAT Gateway first
$nat = gcloud compute routers nats describe $env:CLOUD_NAT_NAME --router=$env:CLOUD_ROUTER_NAME --project=$env:PROJECT_ID --region=$env:REGION --format="value(name)" --quiet
if ($nat) {
    Write-Host "Deleting Cloud NAT Gateway: $($env:CLOUD_NAT_NAME)..."
    gcloud compute routers nats delete $env:CLOUD_NAT_NAME --router=$env:CLOUD_ROUTER_NAME --project=$env:PROJECT_ID --region=$env:REGION --quiet
    Write-Host "✅ Cloud NAT Gateway deleted."
} else {
    Write-Host "INFO: Cloud NAT Gateway '$($env:CLOUD_NAT_NAME)' not found."
}

# Check for and delete the Cloud Router
$router = gcloud compute routers describe $env:CLOUD_ROUTER_NAME --project=$env:PROJECT_ID --region=$env:REGION --format="value(name)" --quiet
if ($router) {
    Write-Host "Deleting Cloud Router: $($env:CLOUD_ROUTER_NAME)..."
    gcloud compute routers delete $env:CLOUD_ROUTER_NAME --project=$env:PROJECT_ID --region=$env:REGION --quiet
    Write-Host "✅ Cloud Router deleted."
} else {
    Write-Host "INFO: Cloud Router '$($env:CLOUD_ROUTER_NAME)' not found."
}


# --- 4. Delete Serverless VPC Access Connector ---
Write-Host "### 4. Deleting Serverless VPC Access Connector... ###"
$connector = gcloud compute networks vpc-access connectors describe $env:VPC_CONNECTOR_NAME --project=$env:PROJECT_ID --region=$env:REGION --format="value(name)" --quiet

if ($connector) {
    Write-Host "Deleting VPC Connector: $($env:VPC_CONNECTOR_NAME)..."
    gcloud compute networks vpc-access connectors delete $env:VPC_CONNECTOR_NAME --project=$env:PROJECT_ID --region=$env:REGION --quiet
    Write-Host "✅ VPC Connector deleted."
} else {
    Write-Host "INFO: VPC Connector '$($env:VPC_CONNECTOR_NAME)' not found."
}

Write-Host "`n--- Teardown Complete ---"