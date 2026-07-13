locals {
  managed_dns_zones = {
    "example.com" = {
      hosted_zone_id = null
    }
  }
  managed_dns_records = {
    "example.com" = {
      zone = "example.com"
      load_balancer = "main"
    }
    "*.example.com" = {
      zone = "example.com"
      load_balancer = "main"
    }
    "k8s.example.com" = {
      zone = "example.com"
      load_balancer = "main"
    }
  }
}

data "aws_route53_zone" "managed" {
  for_each = local.managed_dns_zones
  zone_id = each.value.hosted_zone_id
  name = each.value.hosted_zone_id == null ? each.key : null
  private_zone = each.value.hosted_zone_id == null ? false : null
}

resource "aws_route53_record" "managed_ipv4" {
  for_each = local.managed_dns_records
  zone_id = data.aws_route53_zone.managed[each.value.zone].zone_id
  name = each.key
  type = "A"

  alias {
    name = module.network_123456789012_us_east_1.load_balancers[each.value.load_balancer].dns_name
    zone_id = module.network_123456789012_us_east_1.load_balancers[each.value.load_balancer].zone_id
    evaluate_target_health = false
  }
}

resource "aws_route53_record" "managed_ipv6" {
  for_each = var.enable_ipv6 ? local.managed_dns_records : {}
  zone_id = data.aws_route53_zone.managed[each.value.zone].zone_id
  name = each.key
  type = "AAAA"

  alias {
    name = module.network_123456789012_us_east_1.load_balancers[each.value.load_balancer].dns_name
    zone_id = module.network_123456789012_us_east_1.load_balancers[each.value.load_balancer].zone_id
    evaluate_target_health = false
  }
}
