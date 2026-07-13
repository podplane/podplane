# Runtime configuration inputs. Changes are pushed to existing VMs and applied without replacement,
# which means if you only change and apply one of these, you should only see the Nstance server
# configuration file in the planned changes.

variable "oidc_issuer_url" {
  description = "OIDC issuer; existing VMs are reconfigured."
  type = string
  default = "https://auth.example.com"
}

variable "oidc_signing_algs" {
  description = "OIDC signing algorithms accepted by kube-apiserver; existing VMs are reconfigured. vmconfig defaults to RS256 when unset."
  type = list(string)
  default = ["RS256", "ES256"]
}

variable "kubernetes_api_hostname" {
  description = "Kubernetes API hostname; existing VMs are reconfigured."
  type = string
  default = "test-cluster.k8s.local"
}

variable "kubernetes_api_port" {
  description = "Kubernetes API port; existing VMs are reconfigured."
  type = number
  default = 6443
}

variable "kubernetes_cluster_cidr" {
  description = "Pod CIDRs for control-plane services. Existing VMs are reconfigured, but Podplane does not migrate existing Pods, node CIDR allocations, CNI state, routes, or other networking state."
  type = list(string)
  default = []
}

variable "kubernetes_service_cidr" {
  description = "Default Service CIDRs for control-plane services. Existing VMs are reconfigured, but Podplane does not migrate existing Services or other networking state; additional ServiceCIDR resources are separate."
  type = list(string)
  default = []
}

variable "kubernetes_node_cidr_mask_size_ipv4" {
  description = "IPv4 Pod CIDR prefix for new node allocations. Existing Node.spec.podCIDRs and CNI state are not resized or migrated; old allocations consume corresponding blocks in the new allocation grid."
  type = number
  default = null

  validation {
    condition = var.kubernetes_node_cidr_mask_size_ipv4 == null ? true : (var.kubernetes_node_cidr_mask_size_ipv4 >= 0 && var.kubernetes_node_cidr_mask_size_ipv4 <= 32 && floor(var.kubernetes_node_cidr_mask_size_ipv4) == var.kubernetes_node_cidr_mask_size_ipv4)
    error_message = "kubernetes_node_cidr_mask_size_ipv4 must be a whole number between 0 and 32."
  }
}

variable "kubernetes_node_cidr_mask_size_ipv6" {
  description = "IPv6 Pod CIDR prefix for new node allocations. Existing Node.spec.podCIDRs and CNI state are not resized or migrated; old allocations consume corresponding blocks in the new allocation grid."
  type = number
  default = null

  validation {
    condition = var.kubernetes_node_cidr_mask_size_ipv6 == null ? true : (var.kubernetes_node_cidr_mask_size_ipv6 >= 0 && var.kubernetes_node_cidr_mask_size_ipv6 <= 128 && floor(var.kubernetes_node_cidr_mask_size_ipv6) == var.kubernetes_node_cidr_mask_size_ipv6)
    error_message = "kubernetes_node_cidr_mask_size_ipv6 must be a whole number between 0 and 128."
  }
}

variable "registry_hostname" {
  description = "Registry hostname; existing VMs are reconfigured when set."
  type = string
  default = null
}

variable "ssh_authorized_keys" {
  description = "SSH login keys; existing VMs are reconfigured when set."
  type = string
  default = null
}

variable "kube_api_etcd_servers" {
  description = "etcd endpoints; existing VMs are reconfigured when set."
  type = string
  default = null
}

variable "oidc_ca_cert" {
  description = "OIDC CA certificate; existing VMs are reconfigured when set."
  type = string
  default = null
}

variable "kube_log_level" {
  description = "Kubernetes log level; existing VMs are reconfigured when set."
  type = number
  default = null
}

variable "netsy_endpoint" {
  description = "Netsy endpoint; existing VMs are reconfigured when set."
  type = string
  default = null
}

variable "netsy_access_key_id" {
  description = "Netsy access key; existing VMs are reconfigured when set."
  type = string
  default = null
}

variable "netsy_secret_access_key" {
  description = "Netsy secret key; existing VMs are reconfigured when set."
  type = string
  default = null
}

variable "telemetry_enabled" {
  description = "Telemetry state; existing VMs are reconfigured when set."
  type = bool
  default = null
}

variable "telemetry_log_services" {
  description = "Telemetry services; existing VMs are reconfigured when set."
  type = string
  default = null
}

variable "telemetry_log_cloudinit" {
  description = "Cloud-init log collection; existing VMs are reconfigured when set."
  type = bool
  default = null
}

variable "telemetry_s3_bucket" {
  description = "Telemetry bucket; existing VMs are reconfigured when set."
  type = string
  default = null
}

variable "telemetry_s3_endpoint" {
  description = "Telemetry S3 endpoint; existing VMs are reconfigured when set."
  type = string
  default = null
}

variable "telemetry_s3_assume_role" {
  description = "Telemetry role; existing VMs are reconfigured when set."
  type = string
  default = null
}

variable "telemetry_s3_access_key_id" {
  description = "Telemetry access key; existing VMs are reconfigured when set."
  type = string
  default = null
}

variable "telemetry_s3_secret_access_key" {
  description = "Telemetry secret key; existing VMs are reconfigured when set."
  type = string
  default = null
}

variable "telemetry_otlp_endpoint" {
  description = "OTLP endpoint; existing VMs are reconfigured when set."
  type = string
  default = null
}

variable "registry_enabled" {
  description = "Registry state; existing VMs are reconfigured when set."
  type = bool
  default = null
}

variable "registry_endpoint" {
  description = "Registry endpoint; existing VMs are reconfigured when set."
  type = string
  default = null
}

variable "registry_access_key_id" {
  description = "Registry access key; existing VMs are reconfigured when set."
  type = string
  default = null
}

variable "registry_secret_access_key" {
  description = "Registry secret key; existing VMs are reconfigured when set."
  type = string
  default = null
}

variable "aws_s3_use_path_style" {
  description = "S3 path style; existing VMs are reconfigured when set."
  type = string
  default = null
}

locals {
  oidc_client_id = "test-cluster"
  oidc_username_claim = "email"
  oidc_groups_claim = "groups"
}
