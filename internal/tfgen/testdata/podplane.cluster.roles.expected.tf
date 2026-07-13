locals {
  roles = {
    "netsy" = {
      bucket = "netsy"
      bucket_actions = ["s3:ListBucket", "s3:ListBucketMultipartUploads"]
      object_actions = ["s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:GetObjectAttributes", "s3:AbortMultipartUpload", "s3:ListMultipartUploadParts"]
    }
    "registry-read-only" = {
      bucket = "registry"
      bucket_actions = ["s3:ListBucket", "s3:GetBucketLocation", "s3:ListBucketMultipartUploads"]
      object_actions = ["s3:GetObject", "s3:ListMultipartUploadParts"]
    }
    "registry-read-write" = {
      bucket = "registry"
      bucket_actions = ["s3:ListBucket", "s3:GetBucketLocation", "s3:ListBucketMultipartUploads"]
      object_actions = ["s3:GetObject", "s3:ListMultipartUploadParts", "s3:PutObject", "s3:DeleteObject", "s3:AbortMultipartUpload"]
    }
  }
}

data "aws_iam_policy_document" "assume_from_knc" {
  statement {
    actions = ["sts:AssumeRole"]

    principals {
      type = "AWS"
      identifiers = [module.account_123456789012_us_east_1.agent_iam_role_arn]
    }
  }
}

resource "aws_iam_role" "podplane_cluster" {
  for_each = local.roles
  name = "${local.name_prefix}-${each.key}"
  assume_role_policy = data.aws_iam_policy_document.assume_from_knc.json
}

data "aws_iam_policy_document" "podplane_cluster" {
  for_each = local.roles

  statement {
    actions = each.value.bucket_actions
    resources = [aws_s3_bucket.podplane_cluster[each.value.bucket].arn]
  }

  statement {
    actions = each.value.object_actions
    resources = ["${aws_s3_bucket.podplane_cluster[each.value.bucket].arn}/*"]
  }
}

resource "aws_iam_role_policy" "podplane_cluster" {
  for_each = local.roles
  name = "${local.name_prefix}-${each.key}-policy"
  role = aws_iam_role.podplane_cluster[each.key].id
  policy = data.aws_iam_policy_document.podplane_cluster[each.key].json
}

data "aws_iam_policy_document" "podplane_knc" {
  statement {
    sid = "AssumePodplaneWorkloadRoles"
    actions = ["sts:AssumeRole"]
    resources = [aws_iam_role.podplane_cluster["netsy"].arn, aws_iam_role.podplane_cluster["registry-read-only"].arn, aws_iam_role.podplane_cluster["registry-read-write"].arn]
  }

  statement {
    sid = "DescribeRegions"
    actions = ["ec2:DescribeRegions"]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "podplane_knc" {
  name = "${local.name_prefix}-podplane-knc-policy"
  role = module.account_123456789012_us_east_1.agent_iam_role_name
  policy = data.aws_iam_policy_document.podplane_knc.json
}
