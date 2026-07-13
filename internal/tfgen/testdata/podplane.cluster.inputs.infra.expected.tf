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

variable "enable_ssm" {
  description = "SSM access; changing it updates cloud infrastructure and VM bootstrap."
  type = bool
  default = true
}

locals {
  subnets = {
    "control-plane" = {
      "us-east-1a" = [{ ipv4_cidr = "172.18.1.0/24", nat_subnet = "public" }]
    }
    "nstance" = {
      "us-east-1a" = [{ ipv4_cidr = "172.18.20.0/28", nat_subnet = "public" }]
    }
    "public" = {
      "us-east-1a" = [{ ipv4_cidr = "172.18.10.0/28", public = true, nat_gateway = true }]
    }
  }
  load_balancers = {
    "public-control-plane" = { ports = [7443], subnets = "public", public = true }
  }
}

# Cluster identity and placement. Changing these values is not supported in place;
# destroy the cluster, then update podplane.cluster.jsonc and recreate it.
locals {
  cluster_id = "test-cluster"
  name_prefix = "test-cluster"
  provider_kind = "aws"
  provider_account = "123456789012"
  provider_region = "us-east-1"
}
