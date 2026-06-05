variable "ssh_authorized_key" {
  description = "SSH public key allowed for VM login."
  type = string
  default = ""
}

variable "kube_api_etcd_servers" {
  description = "etcd-compatible endpoint list used by kube-apiserver."
  type = string
  default = ""
}

variable "oidc_custom_ca" {
  description = "Base64-encoded custom OIDC issuer CA certificate."
  type = string
  default = ""
}

variable "oidc_ca_file" {
  description = "OIDC issuer CA file path on the VM."
  type = string
  default = ""
}

variable "kube_log_level" {
  description = "Kubernetes component log verbosity."
  type = number
  default = 2
}

variable "netsy_endpoint" {
  description = "Custom Netsy object-storage endpoint URL."
  type = string
  default = ""
}

variable "netsy_access_key_id" {
  description = "Netsy object-storage access key ID for non-IAM providers."
  type = string
  default = ""
}

variable "netsy_secret_access_key" {
  description = "Netsy object-storage secret access key for non-IAM providers."
  type = string
  default = ""
}

variable "telemetry_enabled" {
  description = "Enable VM telemetry/log forwarding."
  type = bool
  default = false
}

variable "telemetry_log_services" {
  description = "Comma-separated systemd services to include in telemetry logs."
  type = string
  default = ""
}

variable "telemetry_log_cloudinit" {
  description = "Include cloud-init logs in telemetry."
  type = bool
  default = true
}

variable "telemetry_s3_bucket" {
  description = "Telemetry S3 bucket name."
  type = string
  default = ""
}

variable "telemetry_s3_endpoint" {
  description = "Custom telemetry S3 endpoint URL."
  type = string
  default = ""
}

variable "telemetry_s3_assume_role" {
  description = "Telemetry S3 IAM role ARN to assume."
  type = string
  default = ""
}

variable "telemetry_s3_access_key_id" {
  description = "Telemetry S3 access key ID for non-IAM providers."
  type = string
  default = ""
}

variable "telemetry_s3_secret_access_key" {
  description = "Telemetry S3 secret access key for non-IAM providers."
  type = string
  default = ""
}

variable "telemetry_otlp_endpoint" {
  description = "OTLP endpoint for telemetry export."
  type = string
  default = ""
}

variable "registry_enabled" {
  description = "Enable the VM-hosted registry service."
  type = bool
  default = true
}

variable "registry_hostname" {
  description = "Hostname used by clients to reach the registry."
  type = string
  default = ""
}

variable "registry_endpoint" {
  description = "Custom registry object-storage endpoint URL."
  type = string
  default = ""
}

variable "registry_access_key_id" {
  description = "Registry object-storage access key ID for non-IAM providers."
  type = string
  default = ""
}

variable "registry_secret_access_key" {
  description = "Registry object-storage secret access key for non-IAM providers."
  type = string
  default = ""
}

variable "aws_s3_use_path_style" {
  description = "Whether S3 clients should use path-style URLs."
  type = string
  default = ""
}
