#Requires -Version 5.1
<#
.SYNOPSIS
    Deletes temporary test resources and optionally the persistent networking.
#>

# --- Configuration ---
$ErrorActionPreference = 'SilentlyContinue'

# Set this to $true to also delete the VPC Connector.
$deletePersistentNetworking = $true

$projectId = "gemini-power-test"
$region = "europe-west2"
$networkTagMochi = "mochi-test-server"
$vpcConnectorName = "test-vpc-connector"
$cloudRouterName = "test-nat-router"
$cloudNatName = "test-nat-gateway"

# --- 1. Find and Delete GCE VM Instances by Tag ---
Write-Host "### Deleting GCE VMs with tag: $($networkTagMochi)... ###"
$vmInstances = gcloud compute instances list --project=$projectId --filter="tags.items=$($networkTagMochi)" --format="value(name, zone.basename())"
foreach ($vm in $vmInstances) {
    $vmName, $vmZone = $vm -split '\s+'
    Write-Host "Deleting VM '$($vmName)' in zone '$($vmZone)'..."
    gcloud compute instances delete $vmName --project=$projectId --zone=$vmZone --quiet
}

# --- 2. Find and Delete Firewall Rules by Tag ---
Write-Host "### Deleting Firewall Rules with tag: $($networkTagMochi)... ###"
$firewallRules = gcloud compute firewall-rules list --project=$projectId --filter="targetTags.items=$($networkTagMochi)" --format="value(name)"
foreach ($rule in $firewallRules) {
    Write-Host "Deleting Firewall Rule '$($rule)'..."
    gcloud compute firewall-rules delete $rule --project=$projectId --quiet
}

# --- 3. Delete Persistent Networking (Optional) ---
if ($deletePersistentNetworking) {
    Write-Host "### Deleting persistent networking... ###"
    # NAT must be deleted before the Router
    gcloud compute routers nats delete $cloudNatName --router=$cloudRouterName --region=$region --project=$projectId --quiet
    gcloud compute routers delete $cloudRouterName --region=$region --project=$projectId --quiet
    gcloud compute networks vpc-access connectors delete $vpcConnectorName --region=$region --project=$projectId --quiet
} else {
    Write-Host "### Skipping persistent networking deletion as per configuration. ###"
}

Write-Host "`n✅ Teardown complete." -ForegroundColor Green