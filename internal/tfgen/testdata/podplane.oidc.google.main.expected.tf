# OIDC server infrastructure, networking, and optional managed DNS records.

terraform {
  required_version = ">= 1.10.0"

  required_providers {
    google = {
      source = "hashicorp/google"
      version = ">= 6.0"
    }
  }

  backend "gcs" {
  }
}

provider "google" {
  project = "demo-project"
  region = "australia-southeast1"
  zone = "australia-southeast1-b"
}

resource "google_compute_network" "oidc" {
  name = "oidc-auth-exa-c204c854ffe5f35a"
  auto_create_subnetworks = false
}

module "oidc" {
  source = "truster-dev/truster/google"
  version = "3.1.0"
  name_prefix = "oidc-auth-exa-c204c854ffe5f35a"
  network = google_compute_network.oidc.self_link
  oidc_addr = "auth.example.com"
  truster_config = {
    "secrets" = {
      "encryption_key_name" = "projects/demo-project/secrets/encryption/versions/latest"
      "signing_key_name" = "projects/demo-project/secrets/signing/versions/latest"
    }
    "static_policy" = {
      "clients" = {
        "example-cluster" = {}
      }
      "default_redirect_uris" = ["http://localhost:8000"]
    }
    "user_login_connectors" = {
      "github" = {
        "credentials_secret" = "projects/demo-project/secrets/connector/versions/latest"
        "display_name" = "GitHub"
        "type" = "github"
      }
    }
  }
}

resource "google_dns_record_set" "oidc_ipv4" {
  count = module.oidc.enable_ipv4 ? 1 : 0
  managed_zone = "example-com"
  name = "auth.example.com."
  type = "A"
  ttl = 300
  rrdatas = [module.oidc.public_ipv4]
}

resource "google_dns_record_set" "oidc_ipv6" {
  count = module.oidc.enable_ipv6 ? 1 : 0
  managed_zone = "example-com"
  name = "auth.example.com."
  type = "AAAA"
  ttl = 300
  rrdatas = [module.oidc.public_ipv6]
}
