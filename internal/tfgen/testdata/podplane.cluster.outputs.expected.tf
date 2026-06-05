output "cluster_id" {
  value = local.cluster_id
}

output "kubernetes_api_url" {
  value = "https://${local.kubernetes_api_hostname}:${local.kubernetes_api_port}"
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

output "mutable_env" {
  value = local.mutable_env
}

output "registry_read_only_role_arn" {
  value = aws_iam_role.registry_read_only.arn
}

output "registry_read_write_role_arn" {
  value = aws_iam_role.registry_read_write.arn
}

output "netsy_role_arn" {
  value = aws_iam_role.netsy.arn
}
