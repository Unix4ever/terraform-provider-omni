# Look up a service account that was created elsewhere, e.g. with `omnictl serviceaccount create`.
data "omni_service_account" "ci" {
  name = "ci"
}

# The key is deliberately absent: Omni does not store the private half, so only the resource that
# created the account can hand it out.
output "ci_role" {
  value = data.omni_service_account.ci.role
}

output "ci_expires_at" {
  value = data.omni_service_account.ci.expiration
}
