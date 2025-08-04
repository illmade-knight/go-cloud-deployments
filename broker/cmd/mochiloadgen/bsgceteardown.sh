
# --- 5. Deploy/Update Ingestion Service to Use VPC Connector ---
echo "### Configuring ingestion-service to use VPC... ###"
gcloud run deploy ingestion-service \
    --project="${PROJECT_ID}" \
    --region="${REGION}" \
    --image="${REGION}-docker.pkg.dev/${PROJECT_ID}/cloud-deploy/ingestion-service:latest" \
    --vpc-connector="${VPC_CONNECTOR_NAME}" \
    --vpc-egress=all-traffic \
    --update-env-vars="MQTT_BROKER_URL=tcp://${VM_IP}:1883" \
    --platform=managed

# --- 6. Run Your E2E Tests ---
echo "### Running your end-to-end tests... ###"
# Your test command would go here.

# --- 7. Teardown ---
read -p "Tests finished. Press Enter to tear down infrastructure..."

echo "### Deleting GCE instance '${VM_INSTANCE_NAME}'... ###"
gcloud compute instances delete "${VM_INSTANCE_NAME}" --project="${PROJECT_ID}" --zone="${ZONE}" --quiet

echo "### Deleting firewall rule '${FIREWALL_RULE_NAME}'... ###"
gcloud compute firewall-rules delete "${FIREWALL_RULE_NAME}" --project="${PROJECT_ID}" --quiet

echo "### Teardown complete. ###"
