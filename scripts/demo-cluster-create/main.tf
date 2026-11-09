# Podplane <https://podplane.dev>
# Copyright The Podplane Authors
# SPDX-License-Identifier: Apache-2.0

# Local-only playground: no cloud providers, credentials, or network requests.
terraform {
  required_version = ">= 1.4.0"
  backend "local" {}
}

variable "fail" {
  type    = bool
  default = false
}

resource "terraform_data" "resource" {
  for_each = {
    network    = 2
    subnet     = 4
    firewall   = 6
    storage    = 8
    identity   = 10
    controller = 12
    worker     = 16
    gateway    = 20
  }

  input = {
    name        = each.key
    description = "Simulated infrastructure; only a local sleep command runs."
    delay       = each.value
  }

  provisioner "local-exec" {
    command = "sleep ${each.value}; ${var.fail && each.key == "controller" ? "echo 'Intentional demo failure' >&2; exit 1" : "echo 'Local resource ready'"}"
  }
}

output "demo" {
  value = "Local-only apply complete; no cluster was created."
}
