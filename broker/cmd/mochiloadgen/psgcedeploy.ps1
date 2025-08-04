#Requires -Version 5.1
<#
.SYNOPSIS
    Deploys the Mochi server container to a GCE VM directly, without a startup script.
#>

# --- Configuration ---
$ErrorActionPreference = 'Stop'

$projectId = "gemini-power-test"
$region = "europe-west2"
$zone = "$($region)-a"
$arRepo = "cloud-deploy"
$imageName = "mochi-server-gce"
$serviceAccountName = "mochi-vm-sa"
$networkTagMochi = "mochi-test-server"

# --- 1. Build and Push the Updated Application ---
Write-Host "### Building and pushing updated Mochi server image... ###"
gcloud builds submit . --project=$projectId --pack="image=$($region)-docker.pkg.dev/$($projectId)/$($arRepo)/$($imageName):latest"

# --- 2. Deploy the VM and Container Declaratively ---
$vmInstanceName = "mochi-test-vm-$(Get-Date -Format 'yyyyMMdd-HHmmss')"
$sa_email = "$($serviceAccountName)@$($projectId).iam.gserviceaccount.com"
$imageUri = "$($region)-docker.pkg.dev/$($projectId)/$($arRepo)/$($imageName):latest"
$secretName = "projects/$($projectId)/secrets/SERVICE_PASS/versions/latest"

Write-Host "### Creating GCE instance '$($vmInstanceName)' and deploying container... ###"
# This command tells GCE to handle everything: pull the container, and run it with these environment variables.
gcloud compute instances create-with-container $vmInstanceName `
    --project=$projectId `
    --zone=$zone `
    --machine-type="e2-micro" `
    --service-account=$sa_email `
    --tags=$networkTagMochi `
    --container-image=$imageUri `
    --container-env="MQTT_USERNAME=sreceiver,MQTT_PASS_SECRET_NAME=$($secretName)" `
    --container-restart-policy=always `
    --scopes=https://www.googleapis.com/auth/cloud-platform

# --- 3. Provide Instructions ---
$VM_IP = $(gcloud compute instances describe $vmInstanceName --project=$projectId --zone=$zone --format='get(networkInterfaces[0].networkIP)')
Write-Host "`n✅ Deployment Complete: VM '$($vmInstanceName)' is running." -ForegroundColor Green
Write-Host "`n### To trigger the test from Cloud Shell, first start the IAP tunnel in a new terminal: ###" -ForegroundColor Cyan
Write-Host "gcloud compute start-iap-tunnel $($vmInstanceName) 8080 --local-host-port=localhost:8080 --zone=$($zone) --project=$($projectId)"