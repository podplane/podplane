# Cluster endpoints, DNS setup details, and infrastructure references used by the CLI.

output "cluster_id" {
  value = local.cluster_id
}

output "kubernetes_api_url" {
  value = "https://${var.kubernetes_api_hostname}:${var.kubernetes_api_port}"
}

output "domain_targets" {
  description = "Configured domain names and their external routing targets."
  value = {
    "example.com" = {
      names = ["example.com", "*.example.com"]
      target = "cbe15ed8-50dc-4e69-a1d0-9e1909ec4ce8.cfargotunnel.com"
      managed = false
    }
  }
}

output "manual_dns_records" {
  description = "DNS records requiring manual setup. Tunnel DNS and HTTPS edge settings are user-managed."
  value = {
    "example.com" = "cbe15ed8-50dc-4e69-a1d0-9e1909ec4ce8.cfargotunnel.com"
    "*.example.com" = "cbe15ed8-50dc-4e69-a1d0-9e1909ec4ce8.cfargotunnel.com"
    "k8s.example.com" = "cbe15ed8-50dc-4e69-a1d0-9e1909ec4ce8.cfargotunnel.com"
  }
}

output "nstance_bucket" {
  value = module.cluster.bucket
}

output "nstance_shards" {
  value = {
    "us-central1-a" = {
      config_key = module.shard_us_central1_a.config_key
      server_ips = module.shard_us_central1_a.server_ips
    }
  }
}
