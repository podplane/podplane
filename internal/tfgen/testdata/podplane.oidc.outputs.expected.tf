# OIDC issuer URL for configuring cluster authentication.

output "oidc_issuer_url" {
  value = module.oidc.issuer_url
}
