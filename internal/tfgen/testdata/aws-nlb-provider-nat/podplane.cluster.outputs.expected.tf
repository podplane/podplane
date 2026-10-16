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
      target = module.network_123456789012_us_east_1.load_balancers["main"].dns_name
      managed = true
    }
  }
}

output "manual_dns_records" {
  description = "DNS records requiring manual setup. Tunnel DNS and HTTPS edge settings are user-managed."
  value = {}
}

output "nstance_bucket" {
  value = module.cluster.bucket
}

output "nstance_shards" {
  value = {
    "us-east-1a" = {
      config_key = module.shard_us_east_1a.config_key
      server_ips = module.shard_us_east_1a.server_ips
    }
  }
}

output "registry_read_only_role_arn" {
  value = aws_iam_role.podplane_cluster["registry-read-only"].arn
}

output "registry_read_write_role_arn" {
  value = aws_iam_role.podplane_cluster["registry-read-write"].arn
}

output "netsy_role_arn" {
  value = aws_iam_role.podplane_cluster["netsy"].arn
}
