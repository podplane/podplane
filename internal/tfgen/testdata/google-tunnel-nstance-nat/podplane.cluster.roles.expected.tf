# Cloud permissions for cluster storage and secrets, including ingress certificates where needed.

resource "google_storage_bucket_iam_member" "podplane_agent" {
  for_each = google_storage_bucket.podplane_cluster
  bucket = each.value.name
  role = each.key == "registry" ? "roles/storage.objectViewer" : "roles/storage.objectAdmin"
  member = "serviceAccount:${module.account__us_central1.agent_iam_role_arn}"
}

resource "google_storage_bucket_iam_member" "podplane_server_registry" {
  bucket = google_storage_bucket.podplane_cluster["registry"].name
  role = "roles/storage.objectViewer"
  member = "serviceAccount:${module.account__us_central1.server_iam_role_arn}"
}

resource "google_service_account" "podplane_registry" {
  project = local.provider_project
  account_id = "pp-zot-${substr(sha256(module.cluster.bucket), 0, 20)}"
  display_name = "Podplane registry writer"
}

resource "podplane_google_service_account_key" "registry" {
  project = local.provider_project
  service_account = google_service_account.podplane_registry.email
  secret_id = "${google_service_account.podplane_registry.account_id}-key"
}

resource "google_secret_manager_secret_iam_member" "podplane_registry_key" {
  secret_id = podplane_google_service_account_key.registry.id
  role = "roles/secretmanager.secretAccessor"
  member = "serviceAccount:${module.account__us_central1.agent_iam_role_arn}"
}

resource "google_storage_bucket_iam_member" "podplane_registry_writer" {
  bucket = google_storage_bucket.podplane_cluster["registry"].name
  role = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.podplane_registry.email}"
}

resource "google_secret_manager_secret_iam_member" "podplane_workload_ca_key" {
  project = "test-project"
  secret_id = "test-cluster_workload-ca-key"
  role = "roles/secretmanager.secretAccessor"
  member = "serviceAccount:${module.account__us_central1.agent_iam_role_arn}"
  depends_on = [podplane_workload_ca_key.cluster]
}
