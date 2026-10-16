# Cluster infrastructure inputs. Changes may add, replace, or remove provider resources.

variable "vpc_id" {
  description = "Existing VPC ID; changing it replaces network placement."
  type = string
  default = ""
}

variable "vpc_cidr_ipv4" {
  description = "Managed VPC IPv4 CIDR; changing it may replace networking."
  type = string
  default = "172.18.0.0/16"
}

variable "enable_ipv6" {
  description = "IPv6 state; changing it updates cloud networking."
  type = bool
  default = true
}

variable "enable_iap" {
  description = "IAP access to SSH and the Nstance operator API; changing it updates firewall rules."
  type = bool
  default = true
}

variable "nat_mode" {
  description = "Choose provider-managed NAT or Nstance-managed NAT. Switching modes requires updating network routes."
  type = string
  default = "nstance"
}

variable "fixed_public_ipv4_count" {
  description = "Number of static public IPv4 addresses to reserve for each NAT subnet when using Nstance-managed NAT."
  type = number
  default = 2
}

locals {
  subnets = {
    "control-plane" = {
      "us-central1-a" = [{ ipv4_cidr = "172.18.1.0/24", nat_subnet = "public" }]
    }
    "ingress" = {
      "us-central1-a" = [{ ipv4_cidr = "172.18.2.0/24", nat_subnet = "public" }]
    }
    "nstance" = {
      "us-central1-a" = [{ ipv4_cidr = "172.18.20.0/28", public = true }]
    }
    "public" = {
      "us-central1-a" = [{ ipv4_cidr = "172.18.10.0/28", public = true, nat_gateway = true }]
    }
  }
  load_balancers = {}
}

# Cluster identity and placement. Changing these values is not supported in place;
# destroy the cluster, then update podplane.cluster.jsonc and recreate it.
locals {
  cluster_id = "test-cluster"
  spiffe_trust_domain = "k8s.example.com"
  name_prefix = "test-cluster"
  provider_kind = "google"
  provider_project = "test-project"
  provider_region = "us-central1"
}
