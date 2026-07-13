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
    "control-plane" = "t4g.medium"
    "ingress" = "t4g.medium"
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
