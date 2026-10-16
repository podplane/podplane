# VM infrastructure inputs. Changes reconcile VM counts or trigger rolling VM replacement.

variable "immutable_ssh_authorized_keys" {
  description = "Early-boot SSH keys; changing them rolls affected VMs."
  type = string
  default = ""
}

variable "pool_instance_types" {
  description = "Instance types by pool; changing one rolls that pool."
  type = map(string)
  default = {
    "control-plane" = "e2-medium"
    "ingress" = "e2-medium"
    "nat" = "e2-small"
  }
}

variable "pool_sizes" {
  description = "Desired VM counts by pool; changes are reconciled by Nstance."
  type = map(number)
  default = {
    "control-plane" = 1
    "ingress" = 2
  }
}

variable "enable_os_login" {
  description = "IAM-based SSH on Nstance server VMs; changing it rolls those VMs."
  type = bool
  default = true
}
