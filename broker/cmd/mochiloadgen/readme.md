# **Mochi MQTT Server \- Google Cloud Deployment Package**

This package contains a Go application and a suite of automation scripts to deploy a secure, internal-only Mochi MQTT server on Google Cloud Platform (GCP). It is designed to create a reliable, repeatable testing environment for applications that need to connect to an MQTT broker within a GCP VPC network.

The primary deployment method uses the create-with-container feature of the Google Compute Engine (GCE), which provides a robust, declarative way to run containers on VMs without complex startup scripts.

## **Components**

This package includes the following files:

* **runmochiloadgen.go**: The core Go application that runs a Mochi MQTT server. It is configured via environment variables and fetches its MQTT password directly from Google Cloud Secret Manager. It also exposes an HTTP endpoint (/load-test) to initiate a test.
* **setup-environment.ps1 / setup-environment.sh**: One-time setup scripts for preparing your GCP project. They enable all required APIs, create persistent resources (Artifact Registry, Secret Manager), and set all necessary IAM permissions for both the deployment service account and the user.
* **deploy-test-vm.ps1 / deploy-test-vm.sh**: Scripts to deploy a new, temporary GCE virtual machine that runs the Mochi server container.
* **teardown-all.ps1 / teardown-all.sh**: Scripts to clean up all temporary resources created by the deployment script, with an option to also remove persistent networking components to save costs.
* **payload.json**: An example JSON payload to use with curl for triggering the /load-test endpoint.

## **Prerequisites**

Before using these scripts, ensure you have the following tools installed and configured on your local machine:

1. **Google Cloud SDK**: The gcloud command-line tool must be installed and authenticated. You can install it from the [official documentation](https://cloud.google.com/sdk/docs/install).
2. **Go**: The Go programming language (version 1.21 or later) is required to build the Mochi server application.
3. **Authentication**: You must be authenticated with gcloud. Run gcloud auth login and gcloud config set project \[YOUR\_PROJECT\_ID\] to configure the SDK.

## **Deployment Workflow**

Follow these steps to set up the environment and run a test.

### **Step 1: Configure the Scripts**

Before running any scripts, open them and review the configuration variables at the top. 
At a minimum, you should set the $projectId (PowerShell) or PROJECT\_ID (Bash) to match your Google Cloud project ID.

### **Step 2: Run the One-Time Environment Setup**

Choose the script for your preferred shell (.ps1 for PowerShell, .sh for Bash) and run it from your terminal. 
This script will prepare your entire project.

\# PowerShell  
./setup-environment.ps1  
\`\`\`bash  
\# Bash  
chmod \+x setup-environment.sh  
./setup-environment.sh

This script will:

* Enable all necessary Google Cloud APIs.
* Create an Artifact Registry repository named cloud-deploy.
* Create a Secret Manager secret named SERVICE\_PASS with a placeholder value.
* Create a service account (mochi-vm-sa) with permissions to access secrets, read from Artifact Registry, and write logs.
* Grant your user account the permission to create IAP tunnels for testing.
* **Optionally** create a Serverless VPC Connector for your Cloud Run services to use.

**Cost Note:** The setup scripts include a flag ($createPersistentNetworking or CREATE\_PERSISTENT\_NETWORKING) 
that controls whether the costly Serverless VPC Connector is created. Set this to false if you wish to manage it manually.

### **Step 3: Deploy a Test Instance**

Once the environment is prepared, you can deploy a new test VM at any time by running the deployment script.

\# PowerShell  
./deploy-test-vm.ps1  
\`\`\`bash  
\# Bash  
chmod \+x deploy-test-vm.sh  
./deploy-test-vm.sh

This script performs the following actions:

1. Builds the Go application into a container image using Cloud Buildpacks.
2. Pushes the image to your Artifact Registry repository.
3. Creates a temporary firewall rule that allows internal traffic and secure access via IAP.
4. Creates a new GCE VM and declaratively tells it to run your container image, passing the necessary environment variables.

### **Step 4: Run a Test**

After the deployment script finishes, it will print instructions for testing.

1. **Open a new terminal** and start the IAP tunnel. This creates a secure connection from your local machine to the VM.  
   \# This command will be printed by the deploy script  
   gcloud compute start-iap-tunnel \[INSTANCE\_NAME\] 8080 \--local-host-port=localhost:8080 \--zone=\[ZONE\] \--project=\[PROJECT\_ID\]

2. **Leave the tunnel running.** Open a **third terminal** and use curl to send the payload.json file to the server's load test endpoint.  
   curl \-X POST \-H "Content-Type: application/json" \-d @payload.json http://localhost:8080/load-test

### **Step 5: Tear Down the Environment**

When you are finished testing, run the teardown script to remove the temporary resources and avoid unnecessary costs.

\# PowerShell  
./teardown-all.ps1  
\`\`\`bash  
\# Bash  
chmod \+x teardown-all.sh  
./teardown-all.sh

This script will automatically find and delete the GCE VM and the firewall rule created by the deployment script.

**Cost Note:** The teardown scripts include a flag ($deletePersistentNetworking or DELETE\_PERSISTENT\_NETWORKING) 
that controls whether to also delete the Serverless VPC Connector. Set this to true to remove it and stop all related costs.