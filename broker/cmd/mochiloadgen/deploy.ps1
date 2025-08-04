#Requires -Version 5.1
<#
.SYNOPSIS
    Deploys a temporary test VM by building and running the Mochi server container.
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

# --- 1. Build and Push the Application ---
Write-Host "### Building and pushing Mochi server image... ###"
gcloud builds submit . --project=$projectId --pack="image=$($region)-docker.pkg.dev/$($projectId)/$($arRepo)/$($imageName):latest"

# --- 2. Create Temporary Firewall Rule ---
$firewallRuleName = "allow-mochi-test-$(Get-Date -Format 'yyyyMMdd-HHmmss')"
Write-Host "### Creating temporary firewall rule '$($firewallRuleName)'... ###"
gcloud compute firewall-rules create $firewallRuleName `
    --project=$projectId `
    --network="default" `
    --direction="INGRESS" `
    --allow="tcp:1883,tcp:8080" `
    --source-ranges="10.128.0.0/9,35.235.240.0/20" `
    --target-tags=$networkTagMochi

# --- 3. Deploy the VM and Container ---
$vmInstanceName = "mochi-test-vm-$(Get-Date -Format 'yyyyMMdd-HHmmss')"
$sa_email = "$($serviceAccountName)@$($projectId).iam.gserviceaccount.com"
$imageUri = "$($region)-docker.pkg.dev/$($projectId)/$($arRepo)/$($imageName):latest"
$secretName = "projects/$($projectId)/secrets/SERVICE_PASS/versions/latest"

Write-Host "### Creating GCE instance '$($vmInstanceName)' and deploying container... ###"
gcloud compute instances create-with-container $vmInstanceName `
    --project=$projectId `
    --zone=$zone `
    --machine-type="e2-micro" `
    --service-account=$sa_email `
    --tags=$networkTagMochi `
    --scopes=https://www.googleapis.com/auth/cloud-platform `
    --container-image=$imageUri `
    --container-env="MQTT_USERNAME=sreceiver,MQTT_PASS_SECRET_NAME=$($secretName)" `
    --container-restart-policy=always

# --- 4. Provide Instructions ---
$VM_IP = $(gcloud compute instances describe $vmInstanceName --project=$projectId --zone=$zone --format='get(networkInterfaces[0].networkIP)')
Write-Host "`n✅ Deployment Complete: VM '$($vmInstanceName)' is running at internal IP ${VM_IP}." -ForegroundColor Green
Write-Host "`n### To trigger the test from Cloud Shell, first start the IAP tunnel in a new terminal: ###" -ForegroundColor Cyan
Write-Host "gcloud compute start-iap-tunnel $($vmInstanceName) 8080 --local-host-port=localhost:8080 --zone=$($zone) --project=$($projectId)"