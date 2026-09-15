# An infrastructure provider: the InfraProvider resource a machine class's `auto_provision` block
# points at via `provider_id`, plus the service account (named `infra-provider:proxmox`) that the
# provider authenticates to Omni as.
resource "omni_infra_provider" "proxmox" {
  name = "proxmox"
  ttl  = "2160h" # 90 days
}

# The key is what OMNI_SERVICE_ACCOUNT_KEY expects. Omni never returns it again, so it lives only in
# Terraform state: keep the state secret. Pass it to the infra provider you run alongside this
# Terraform configuration.
output "proxmox_infra_provider_key" {
  value     = omni_infra_provider.proxmox.key
  sensitive = true
}

# Renewing on a schedule. Omni issues a new key while the provider keeps its identity, and the
# previous key stays valid until it expires.
resource "time_rotating" "proxmox_key" {
  rotation_days = 60
}

resource "omni_infra_provider" "proxmox_rotating" {
  name = "proxmox-rotating"
  ttl  = "2160h" # 90 days

  renew_trigger = time_rotating.proxmox_key.rfc3339
}
