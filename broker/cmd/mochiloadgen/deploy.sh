#!/bin/bash
# This script automates the setup and teardown of a secure, internal-only GCE VM
# to run the Mochi MQTT server for end-to-end tests.

# Exit immediately if a command exits with a non-zero status.
set -e

# --- Configuration ---
export PROJECT_ID="gemini-power-test"
export REGION="europe-west2"
export ZONE="${REGION}-a"
export AR_REPO="cloud-deploy"
export IMAGE_NAME="mochi-server-gce"
# Use a compliant timestamp format for the VM name
export VM_INSTANCE_NAME="mochi-test-vm-$(date +%Y%m%d-%H%M%S)"
export FIREWALL_RULE_NAME="allow-internal-mqtt-for-test"
export VPC_CONNECTOR_NAME="test-vpc-connector"
export NETWORK_TAG_MOCHI="mochi-test-server"

# --- One-Time Setup: Create VPC Connector (if it doesn't exist) ---
echo "### Checking for VPC Access Connector '${VPC_CONNECTOR_NAME}'... ###"
if ! gcloud compute networks vpc-access connectors describe "${VPC_CONNECTOR_NAME}" \
  --project="${PROJECT_ID}" \
  --region="${REGION}" >/dev/null 2>&1; then
  echo "Connector not found, creating it now..."
  gcloud compute networks vpc-access connectors create "${VPC_CONNECTOR_NAME}" \
    --project="${PROJECT_ID}" \
    --region="${REGION}" \
    --network=default \
    --range=10.8.0.0/28
else
    echo "VPC Connector already exists."
fi

# --- One-Time Setup: Enable Private Google Access ---
Write-Host "### Enabling Private Google Access for the default subnet in $($env:REGION)... ###"
gcloud compute networks subnets update default `
    --project=$env:PROJECT_ID `
    --region=$env:REGION `
    --enable-private-ip-google-access

# --- One-Time Setup: Create Cloud Router and NAT Gateway (if they don't exist) ---
Write-Host "### Checking for Cloud Router '$($env:CLOUD_ROUTER_NAME)'... ###"
try {
    gcloud compute routers describe $env:CLOUD_ROUTER_NAME --project=$env:PROJECT_ID --region=$env:REGION -q | Out-Null
    Write-Host "Cloud Router already exists."
}
catch {
    Write-Host "Cloud Router not found, creating it now..."
    gcloud compute routers create $env:CLOUD_ROUTER_NAME --project=$env:PROJECT_ID --region=$env:REGION --network=default --asn=64512
}

Write-Host "### Checking for Cloud NAT Gateway '$($env:CLOUD_NAT_NAME)'... ###"
try {
    gcloud compute routers nats describe $env:CLOUD_NAT_NAME --router=$env:CLOUD_ROUTER_NAME --project=$env:PROJECT_ID --region=$env:REGION -q | Out-Null
    Write-Host "Cloud NAT Gateway already exists."
}
catch {
    Write-Host "Cloud NAT Gateway not found, creating it now..."
    gcloud compute routers nats create $env:CLOUD_NAT_NAME --router=$env:CLOUD_ROUTER_NAME --project=$env:PROJECT_ID --region=$env:REGION --auto-allocate-nat-external-ips --nat-all-subnet-ip-ranges
}

# --- 1. Build and Push the Docker Image (Using Buildpacks) ---
echo "### Building and pushing Mochi server image... ###"
gcloud builds submit . \
  --project="${PROJECT_ID}" \
  --pack image="${REGION}-docker.pkg.dev/${PROJECT_ID}/${AR_REPO}/${IMAGE_NAME}:latest"

# --- 2. Create Secure Firewall Rule ---
echo "### Creating secure firewall rule '${FIREWALL_RULE_NAME}'... ###"
gcloud compute firewall-rules create "${FIREWALL_RULE_NAME}" \
    --project="${PROJECT_ID}" \
    --direction=INGRESS \
    --priority=1000 \
    --network=default \
    --action=ALLOW \
    --rules=tcp:1883,tcp:8080 \
    --source-ranges=10.128.0.0/9 \
    --target-tags="${NETWORK_TAG_MOCHI}"

# --- 3. Create the Internal-Only GCE VM ---
echo "### Creating GCE instance '${VM_INSTANCE_NAME}' with no external IP... ###"
gcloud compute instances create "${VM_INSTANCE_NAME}" \
    --project="${PROJECT_ID}" \
    --zone="${ZONE}" \
    --machine-type=e2-micro \
    --image-family=debian-11 \
    --image-project=debian-cloud \
    --boot-disk-size=10GB \
    --no-address \
    --tags="${NETWORK_TAG_MOCHI}" \
    --metadata="region=${REGION},ar-repo=${AR_REPO},image-name=${IMAGE_NAME},mqtt-user=sreceiver,mqtt-pass-secret-name=projects/${PROJECT_ID}/secrets/SERVICE_PASS/versions/latest" \
    --metadata-from-file=startup-script=startup-script.sh

# --- 4. Get the VM's INTERNAL IP Address ---
echo "### Fetching internal IP... ###"
VM_IP=$(gcloud compute instances describe "${VM_INSTANCE_NAME}" \
    --project="${PROJECT_ID}" \
    --zone="${ZONE}" \
    --format='get(networkInterfaces[0].networkIP)')

echo "### Mochi server is running at internal address: tcp://${VM_IP}:1883 ###"
echo "### Load test trigger is available at: http://${VM_IP}:8080/load-test ###"
