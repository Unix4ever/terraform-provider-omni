# A service account for CI, scoped to the least privilege it needs.
resource "omni_service_account" "ci" {
  name = "ci"
  role = "Operator"
  ttl  = "2160h" # 90 days
}

# Leaving the role unset inherits the role of the identity Terraform authenticates as. The role
# Omni settled on is read back into state.
resource "omni_service_account" "automation" {
  name = "automation"
}

# The key is what OMNI_SERVICE_ACCOUNT_KEY expects. Omni never returns it again, so it lives only in
# Terraform state: keep the state secret, and rotate in place by renewing -- change `ttl`, or change
# `renew_trigger` as the schedule below does -- rather than by replacing the resource.
output "ci_service_account_key" {
  value     = omni_service_account.ci.key
  sensitive = true
}

# `expiration` is the signal to renew before the key stops working.
output "ci_service_account_expires_at" {
  value = omni_service_account.ci.expiration
}

# Renewing on a schedule. Omni issues a new key while the account keeps its identity, and the
# previous key stays valid until it expires, so consumers still holding it keep working until then.
#
# Rotating every 60 days, well inside the 90-day ttl above, leaves a 30-day overlap in which to roll
# the new key out to whoever consumes it.
resource "time_rotating" "ci_key" {
  rotation_days = 60
}

resource "omni_service_account" "ci_rotating" {
  name = "ci-rotating"
  role = "Operator"
  ttl  = "2160h" # 90 days

  renew_trigger = time_rotating.ci_key.rfc3339
}

# Bootstrapping: create a service account and immediately use it, in the same apply.
#
# The root provider authenticates with whatever credentials Terraform was given (typically
# OMNI_SERVICE_ACCOUNT_KEY from an admin account). A second, aliased provider then authenticates as
# the account created above, so anything built through it is owned by that narrower identity.
#
# Terraform resolves the provider configuration after the service account is created, so this needs
# no second apply, and `terraform destroy` unwinds it in the right order.
provider "omni" {
  alias               = "deployer"
  service_account_key = omni_service_account.ci.key
}

resource "omni_cluster" "example" {
  provider = omni.deployer

  name               = "example"
  kubernetes_version = "1.36.2"
  talos_version      = "1.13.5"
}
