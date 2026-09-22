terraform {
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      source = "hashicorp/aws"
      version = ">= 6.0"
    }
  }
}

provider "aws" {
  region = "us-east-1"
}

data "aws_route53_zone" "oidc" {
  name = "example.com."
}

resource "aws_vpc" "oidc" {
  cidr_block = "10.0.0.0/16"
  assign_generated_ipv6_cidr_block = true
  enable_dns_hostnames = true
  enable_dns_support = true
  tags = {
    Name = "truster-vpc"
  }
}

resource "aws_internet_gateway" "oidc" {
  vpc_id = aws_vpc.oidc.id
  tags = {
    Name = "truster-igw"
  }
}

resource "aws_route_table" "oidc" {
  vpc_id = aws_vpc.oidc.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.oidc.id
  }

  route {
    ipv6_cidr_block = "::/0"
    gateway_id = aws_internet_gateway.oidc.id
  }
  tags = {
    Name = "truster-rt"
  }
}

module "oidc" {
  source = "truster/truster/aws"
  vpc_id = aws_vpc.oidc.id
  oidc_addr = "auth.example.com"
  secrets_provider = "aws-secrets-manager"
  truster_config = {
    secrets = {
      signing_key_name = "arn:signing"
      encryption_key_name = "arn:encryption"
    }
    user_login_connectors = {
      "google" = {
        type = "google"
        display_name = "Google"
        credentials_secret = "arn:connector"
      }
    }
    static_policy = {
      default_redirect_uris = ["http://localhost:8000"]
      clients = {
        "kubelogin" = {
          user_group_mapping = "production"
        }
      }
      user_group_mappings = {
        "production" = {
          "ops@example.com" = ["admins"]
        }
      }
    }
  }
}

resource "aws_route_table_association" "oidc" {
  subnet_id = module.oidc.subnet_id
  route_table_id = aws_route_table.oidc.id
}

resource "aws_route53_record" "oidc_ipv4" {
  count = module.oidc.enable_ipv4 ? 1 : 0
  zone_id = data.aws_route53_zone.oidc.zone_id
  name = "auth.example.com"
  type = "A"
  ttl = 300
  records = [module.oidc.public_ipv4]
}

resource "aws_route53_record" "oidc_ipv6" {
  count = module.oidc.enable_ipv6 ? 1 : 0
  zone_id = data.aws_route53_zone.oidc.zone_id
  name = "auth.example.com"
  type = "AAAA"
  ttl = 300
  records = [module.oidc.public_ipv6]
}
