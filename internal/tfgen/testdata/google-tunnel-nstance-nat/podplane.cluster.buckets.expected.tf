# Object storage buckets for Netsy cluster state and the container registry.

resource "google_storage_bucket" "podplane_cluster" {
  for_each = {
    "netsy" = local.netsy_bucket_name
    "registry" = local.registry_bucket_name
  }
  name = each.value
  project = local.provider_project
  location = local.provider_region
  uniform_bucket_level_access = true
  public_access_prevention = "enforced"
}
