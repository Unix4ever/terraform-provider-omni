# The media parameters: what goes into the image.
resource "omni_installation_media_preset" "metal" {
  name          = "metal-production"
  architecture  = "amd64"
  talos_version = "1.13.5"

  extensions = ["siderolabs/qemu-guest-agent"]

  machine_labels = {
    env = "production"
  }
}

# Resolve the preset into something that can actually be fetched. Omni registers the schematic with
# the image factory and assembles the URL, so nothing here encodes the factory's layout.
data "omni_installation_media" "iso" {
  preset = omni_installation_media_preset.metal.name
  format = "iso"

  # Proxmox fetches the file itself, from a queue, so the URL has to still work by the time that
  # runs rather than only while Terraform is applying.
  download_token_ttl = "1h"
}

# Pull the ISO into a Proxmox datastore.
#
# file_name is keyed on storage_key, not on the URL: against an authenticated image factory Omni
# mints a fresh download token on every read, so the URL changes every plan while the medium it
# points at does not. Naming the file after the URL would re-download the same ISO forever.
#
# ISO rather than `format = "raw"` on purpose: this resource decompresses only gz, lzo, zst and bz2,
# and a raw Talos image arrives as raw.xz.
resource "proxmox_virtual_environment_download_file" "talos" {
  content_type = "iso"
  datastore_id = "local"
  node_name    = "pve"

  url       = data.omni_installation_media.iso.url
  file_name = "talos-${substr(data.omni_installation_media.iso.storage_key, 0, 16)}.iso"
}

resource "proxmox_virtual_environment_vm" "worker" {
  count     = 3
  node_name = "pve"

  cdrom {
    file_id = proxmox_virtual_environment_download_file.talos.id
  }
}

# For bare metal, the same preset yields an iPXE script URL instead.
data "omni_installation_media" "pxe" {
  preset = omni_installation_media_preset.metal.name
  format = "pxe"
}
