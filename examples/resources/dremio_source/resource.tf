resource "dremio_source" "gcs_example" {
  name = "bucket"
  type = "GCS"

  config {
    project_id       = "my-gcp-project"
    auth_mode        = "AUTO" # or "SERVICE_ACCOUNT_KEYS" with secure_config.private_key
    root_path        = "/"
    bucket_whitelist = ["my-bucket"]
  }
}
