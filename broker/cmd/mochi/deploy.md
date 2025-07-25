# services.yaml

### Environment defines global settings for the entire architecture.
environment:
region: "eu-west2"
### The image repo where Cloud Build will store the service images.
image_repo: "gcm-images"

### service_manager_spec describes the ServiceDirector service itself.
#### The Conductor will deploy this service first.
service_manager_spec:
name: "servicedirector-bq-flow"
service_account: "servicedirector-sa"
deployment:
source_path: "cmd/servicedirector" # Path to the ServiceDirector main package
buildable_module_path: "."
environment_vars:
#### These topics are used for communication between the Conductor and the Director.
SD_COMMAND_TOPIC: "director-commands-bq"
SD_COMPLETION_TOPIC: "director-events-bq"
SD_COMMAND_SUBSCRIPTION: "director-command-sub-bq"

#### dataflows describes one or more independent data processing pipelines.
dataflows:
bigquery-flow:
#### services lists the application microservices to be deployed.
services:
ingestion-service:
name: "ingestion-service"
service_account: "ingestion-sa"
deployment:
source_path: "cmd/ingestion_service" # Path to your ingestion service code
buildable_module_path: "."
secret_environment_vars:
- name: "APP_MQTT_PASSWORD"
value_from: "SERVICE_PASS" # The name of the secret in Secret Manager
environment_vars:
#### The service needs to know which Pub/Sub topic to publish to.
TOPIC_ID: "ingestion-topic-bq"
#### Add other necessary config like MQTT broker URL
APP_MQTT_BROKER_URL: "tcp://mochi-885150127230.us-central1.run.app:1883"
APP_MQTT_USERNAME: "sreceiver"

      bigquery-service:
        name: "bigquery-service"
        service_account: "bigquery-sa"
        deployment:
          source_path: "cmd/bigquery_service" # Path to your BigQuery service code
          buildable_module_path: "."
          environment_vars:
            # The service needs to know which subscription and BQ table to use.
            SUBSCRIPTION_ID: "bq-subscription"
            DATASET_ID: "iot_data_bq"
            TABLE_ID: "ingested_payloads_bq"

    # resources lists the cloud infrastructure the ServiceDirector must create
    # BEFORE the application services are deployed.
    resources:
      topics:
        - name: "ingestion-topic-bq"
      subscriptions:
        - name: "bq-subscription"
          topic: "ingestion-topic-bq"
      bigquery_datasets:
        - name: "iot_data_bq"
      bigquery_tables:
        - name: "ingested_payloads_bq"
          dataset: "iot_data_bq"
          # The ServiceDirector uses this to find the schema struct in its code.
          schema_source_identifier: "github.com/your-repo/path/to.YourPayloadStruct"
          # Example of adding clustering for performance.
          clustering_fields: ["device_id"]