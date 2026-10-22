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

resource "google_project_service" "workload_identity" {
  project = local.provider_project
  service = "sts.googleapis.com"
  disable_on_destroy = false
}

resource "google_iam_workload_identity_pool" "cluster" {
  project = local.provider_project
  workload_identity_pool_id = "pp-${substr(sha256(module.cluster.bucket), 0, 24)}"
  display_name = substr("Podplane ${local.cluster_id}", 0, 32)
  depends_on = [google_project_service.workload_identity]
}

resource "google_iam_workload_identity_pool_provider" "cluster" {
  project = local.provider_project
  workload_identity_pool_id = google_iam_workload_identity_pool.cluster.workload_identity_pool_id
  workload_identity_pool_provider_id = "kubernetes"
  attribute_mapping = {
    "google.subject" = "assertion.sub"
  }
  attribute_condition = "assertion.sub.startsWith('system:serviceaccount:')"

  oidc {
    issuer_uri = "https://${var.kubernetes_api_hostname}:${var.kubernetes_api_port}"
    allowed_audiences = ["https://iam.googleapis.com/${google_iam_workload_identity_pool.cluster.name}/providers/kubernetes"]
    jwks_json = podplane_service_account_key.cluster.jwks_json
  }
}

resource "google_storage_bucket_iam_member" "podplane_registry_writer" {
  bucket = google_storage_bucket.podplane_cluster["registry"].name
  role = "roles/storage.objectAdmin"
  member = "principal://iam.googleapis.com/${google_iam_workload_identity_pool.cluster.name}/subject/system:serviceaccount:platform-zot-registry:zot-registry"
}

locals {
  google_secrets_providers = {
    "google-secret-manager" = {
      project = "test-project"
      prefix = "test-cluster"
    }
  }
}

data "google_project" "secrets" {
  for_each = local.google_secrets_providers
  project_id = each.value.project
}

resource "google_project_iam_custom_role" "podplane_secrets_catalog" {
  for_each = local.google_secrets_providers
  project = each.value.project
  role_id = "pp_${substr(sha256(join("/", [module.cluster.bucket, each.key])), 0, 20)}_secrets_catalog"
  title = "Podplane secrets_catalog"
  permissions = ["secretmanager.secrets.create", "secretmanager.secrets.list"]
}

resource "google_project_iam_member" "podplane_secrets_catalog" {
  for_each = local.google_secrets_providers
  project = each.value.project
  role = google_project_iam_custom_role.podplane_secrets_catalog[each.key].id
  member = "principal://iam.googleapis.com/${google_iam_workload_identity_pool.cluster.name}/subject/system:serviceaccount:platform-podplane-operator:platform-podplane-operator"
}

resource "google_project_iam_custom_role" "podplane_secrets_lifecycle" {
  for_each = local.google_secrets_providers
  project = each.value.project
  role_id = "pp_${substr(sha256(join("/", [module.cluster.bucket, each.key])), 0, 20)}_secrets_lifecycle"
  title = "Podplane secrets_lifecycle"
  permissions = ["secretmanager.secrets.get", "secretmanager.secrets.delete", "secretmanager.versions.get", "secretmanager.versions.list", "secretmanager.versions.add", "secretmanager.versions.disable", "secretmanager.versions.enable", "secretmanager.versions.destroy"]
}

resource "google_project_iam_member" "podplane_secrets_lifecycle" {
  for_each = local.google_secrets_providers
  project = each.value.project
  role = google_project_iam_custom_role.podplane_secrets_lifecycle[each.key].id
  member = "principal://iam.googleapis.com/${google_iam_workload_identity_pool.cluster.name}/subject/system:serviceaccount:platform-podplane-operator:platform-podplane-operator"

  condition {
    title = "PodplaneSecretScope"
    expression = "resource.name.startsWith(\"projects/${data.google_project.secrets[each.key].number}/secrets/${each.value.prefix}_\") && resource.name != \"projects/${data.google_project.secrets[each.key].number}/secrets/${each.value.prefix}_workload-ca-key\" && !resource.name.startsWith(\"projects/${data.google_project.secrets[each.key].number}/secrets/${each.value.prefix}_workload-ca-key/\")"
  }
}

resource "google_project_iam_custom_role" "podplane_secrets_reader" {
  for_each = local.google_secrets_providers
  project = each.value.project
  role_id = "pp_${substr(sha256(join("/", [module.cluster.bucket, each.key])), 0, 20)}_secrets_reader"
  title = "Podplane secrets_reader"
  permissions = ["secretmanager.versions.access"]
}

resource "google_project_iam_member" "podplane_secrets_reader" {
  for_each = local.google_secrets_providers
  project = each.value.project
  role = google_project_iam_custom_role.podplane_secrets_reader[each.key].id
  member = "principal://iam.googleapis.com/${google_iam_workload_identity_pool.cluster.name}/subject/system:serviceaccount:platform-secrets-store-csi-provider-gcp:platform-secrets-store-csi-provider-gcp"

  condition {
    title = "PodplaneSecretScope"
    expression = "resource.name.startsWith(\"projects/${data.google_project.secrets[each.key].number}/secrets/${each.value.prefix}_\")"
  }
}

resource "google_project_iam_custom_role" "podplane_ingress_state" {
  for_each = local.google_secrets_providers
  project = each.value.project
  role_id = "pp_${substr(sha256(join("/", [module.cluster.bucket, each.key])), 0, 20)}_ingress_state"
  title = "Podplane ingress_state"
  permissions = ["secretmanager.versions.access"]
}

resource "google_project_iam_member" "podplane_ingress_state" {
  for_each = local.google_secrets_providers
  project = each.value.project
  role = google_project_iam_custom_role.podplane_ingress_state[each.key].id
  member = "principal://iam.googleapis.com/${google_iam_workload_identity_pool.cluster.name}/subject/system:serviceaccount:platform-podplane-operator:platform-podplane-operator"

  condition {
    title = "PodplaneSecretScope"
    expression = "resource.name.startsWith(\"projects/${data.google_project.secrets[each.key].number}/secrets/${each.value.prefix}_platform-cluster_ingress-certificates_\")"
  }
}

resource "google_project_iam_member" "csi_gce_pd_storage" {
  project = local.provider_project
  role = "roles/compute.storageAdmin"
  member = "principal://iam.googleapis.com/${google_iam_workload_identity_pool.cluster.name}/subject/system:serviceaccount:platform-csi-gce-pd:platform-csi-gce-pd-controller"
}

resource "google_project_iam_custom_role" "csi_gce_pd" {
  project = local.provider_project
  role_id = "pp_${substr(sha256(module.cluster.bucket), 0, 20)}_csi_gce_pd"
  title = "Podplane PD CSI instance access"
  permissions = ["compute.instances.get", "compute.instances.attachDisk", "compute.instances.detachDisk"]
}

resource "google_project_iam_member" "csi_gce_pd_instances" {
  project = local.provider_project
  role = google_project_iam_custom_role.csi_gce_pd.id
  member = "principal://iam.googleapis.com/${google_iam_workload_identity_pool.cluster.name}/subject/system:serviceaccount:platform-csi-gce-pd:platform-csi-gce-pd-controller"
}

resource "google_service_account_iam_member" "csi_gce_pd_agent" {
  service_account_id = "projects/${local.provider_project}/serviceAccounts/${module.account__us_central1.agent_iam_role_arn}"
  role = "roles/iam.serviceAccountUser"
  member = "principal://iam.googleapis.com/${google_iam_workload_identity_pool.cluster.name}/subject/system:serviceaccount:platform-csi-gce-pd:platform-csi-gce-pd-controller"
}
