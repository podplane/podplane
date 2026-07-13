locals {
  buckets = {
    "netsy" = local.netsy_bucket_name
    "registry" = local.registry_bucket_name
  }
}

resource "aws_s3_bucket" "podplane_cluster" {
  for_each = local.buckets
  bucket = each.value
}

resource "aws_s3_bucket_public_access_block" "podplane_cluster" {
  for_each = local.buckets
  bucket = aws_s3_bucket.podplane_cluster[each.key].id
  block_public_acls = true
  block_public_policy = true
  ignore_public_acls = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "podplane_cluster" {
  for_each = local.buckets
  bucket = aws_s3_bucket.podplane_cluster[each.key].id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}
