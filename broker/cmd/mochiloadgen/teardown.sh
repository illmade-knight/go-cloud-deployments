#!/bin/bash
# Deletes temporary test resources and optionally the persistent networking.

set -e

# --- Configuration ---
# Set this to true to also delete the VPC Connector.
DELETE_PERSISTENT_NETWORKING=true

PROJECT_ID="gemini-power-test"
REGION="europe-west2"
NETWORK_TAG="mochi-test-server"
VPC_CONNECTOR_NAME="test-vpc-connector"
CLOUD_ROUTER_NAME="test-nat-router"
CLOUD_NAT_NAME="test-nat-gateway"

# --- 1. Find and Delete GCE VM Instances by Tag ---
echo "### Deleting GCE VMs with tag: ${NETWORK_TAG}... ###"
# Using a while loop to correctly handle multi-line output from gcloud
gcloud compute instances list --project="$PROJECT_ID" --filter="tags.items=${NETWORK_TAG}" --format="value(name,zone)" | \
while read -r VM_NAME ZONE; do
  if [ -n "$VM_NAME" ]; then
    echo "Deleting VM '${VM_NAME}' in zone '${ZONE}'..."
    gcloud compute instances delete "$VM_NAME" --project="$PROJECT_ID" --zone="$ZONE" --quiet
  fi
done

# --- 2. Find and Delete Firewall Rules by Tag ---
echo "### Deleting Firewall Rules with tag: ${NETWORK_TAG}... ###"
FIREWALL_RULES=$(gcloud compute firewall-rules list --project="$PROJECT_ID" --filter="targetTags.items=${NETWORK_TAG}" --format="value(name)")
for RULE in $FIREWALL_RULES; do
  echo "Deleting Firewall Rule '${RULE}'..."
  gcloud compute firewall-rules delete "$RULE" --project="$PROJECT_ID" --quiet
done

# --- 3. Delete Persistent Networking (Optional) ---
if [ "$DELETE_PERSISTENT_NETWORKING" = true ]; then
    echo "### Deleting persistent networking... ###"
    # Use '|| true' to prevent script from exiting if resource doesn't exist
    gcloud compute routers nats delete "$CLOUD_NAT_NAME" --router="$CLOUD_ROUTER_NAME" --region="$REGION" --project="$PROJECT_ID" --quiet || true
    gcloud compute routers delete "$CLOUD_ROUTER_NAME" --region="$REGION" --project="$PROJECT_ID" --quiet || true
    gcloud compute networks vpc-access connectors delete "$VPC_CONNECTOR_NAME" --region="$REGION" --project="$PROJECT_ID" --quiet || true
else
    echo "### Skipping persistent networking deletion as per configuration. ###"
fi

echo -e "\n✅ Teardown complete."