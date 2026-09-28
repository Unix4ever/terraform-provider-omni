## [Omni Terraform Provider 0.1.0-beta.0](https://github.com/siderolabs/terraform-provider-omni/releases/tag/v0.1.0-beta.0) (2026-09-28)

Welcome to the v0.1.0-beta.0 release of Omni Terraform Provider!  
*This is a pre-release of Omni Terraform Provider*



Please try out the release binaries and report any issues at
https://github.com/siderolabs/terraform-provider-omni/issues.

### etcd Backup S3 Configuration

The new `omni_etcd_backup_s3_config` resource manages the instance-wide S3 target for etcd backups, complementing the per-cluster `backup_interval` on `omni_cluster`.


### Infrastructure Providers

The new `omni_infra_provider` resource registers an infrastructure provider in Omni together with its service account and exposes the generated key for the provider to authenticate with.


### Installation Media Presets

The new `omni_installation_media_preset` resource manages installation media presets from Terraform.


### Machine Classes

The new `omni_machine_class` resource manages machine classes that either match existing machines by labels or auto-provision them through an infrastructure provider.


### Machine Install Disk

The new `omni_machine_install_disk` resource sets a machine's Talos install disk, either by device path or by a CEL disk selector expression.


### Service Accounts

The new `omni_service_account` resource and data source manage Omni service accounts from Terraform.


### Contributors

* Mateusz Urbanek
* Artem Chernyshev
* Justin Garrison
* Nate O'Farrell

### Changes
<details><summary>7 commits</summary>
<p>

* [`ce78e87`](https://github.com/siderolabs/terraform-provider-omni/commit/ce78e87bcac2962dd0eb914e72615dd794d76fcb) feat: add omni_machine_install_disk resource
* [`8ea6816`](https://github.com/siderolabs/terraform-provider-omni/commit/8ea68163be26bf5499665bf06afb32698eb1bab3) feat: add infra_provider resource
* [`686b15d`](https://github.com/siderolabs/terraform-provider-omni/commit/686b15d59a758e970ccda693486c0c54ebe0571c) fix: keep configured name on named machine sets
* [`f6973bf`](https://github.com/siderolabs/terraform-provider-omni/commit/f6973bf9c99df21763402ca9479d7dda79b98bda) feat: add service account support
* [`f40ebb5`](https://github.com/siderolabs/terraform-provider-omni/commit/f40ebb583d119b64f21f27091e6f17fdccfd2766) feat: add omni_machine_class resource
* [`395fd29`](https://github.com/siderolabs/terraform-provider-omni/commit/395fd299609566b52d95992373f5cbd4b15ee3f6) feat: implement installation media preset support
* [`1269e66`](https://github.com/siderolabs/terraform-provider-omni/commit/1269e661638a412f09c6bd6874ca3f901d7f434c) feat: add omni_etcd_backup_s3_config resource
</p>
</details>

### Dependency Changes

* **github.com/cosi-project/runtime**    v1.16.2 -> v1.16.3
* **github.com/google/uuid**             v1.6.0 **_new_**
* **github.com/siderolabs/gen**          v0.8.6 -> v0.8.7
* **github.com/siderolabs/omni/client**  49c8e725f6a1 -> v1.12.2
* **github.com/stretchr/testify**        v1.12.1 **_new_**
* **google.golang.org/protobuf**         f2248ac996af -> v1.36.12

Previous release can be found at [v0.1.0-alpha.3](https://github.com/siderolabs/terraform-provider-omni/releases/tag/v0.1.0-alpha.3)

## [Omni Terraform Provider 0.1.0-alpha.0](https://github.com/siderolabs/terraform-provider-omni/releases/tag/v0.1.0-alpha.0) (2026-07-08)

Welcome to the v0.1.0-alpha.0 release of Omni Terraform Provider!



Please try out the release binaries and report any issues at
https://github.com/siderolabs/terraform-provider-omni/issues.

### Contributors

* Artem Chernyshev

### Changes
<details><summary>4 commits</summary>
<p>

* [`4d0dc31`](https://github.com/siderolabs/terraform-provider-omni/commit/4d0dc31a2b5a777ee3cd508e17ff9c0065c4b1e7) feat: add cluster management support
* [`e66ef24`](https://github.com/siderolabs/terraform-provider-omni/commit/e66ef2492c5118de0d9c1f79415d58726270d998) feat: initial version of the provider that supports user management
* [`e3fb9f2`](https://github.com/siderolabs/terraform-provider-omni/commit/e3fb9f2674ae1d70fae8812379577c84aaed3b1e) test: add GHA
* [`3fcdd05`](https://github.com/siderolabs/terraform-provider-omni/commit/3fcdd0580d33ca812b5f8eebdb3fbfedd11a92f1) first commit
</p>
</details>

### Dependency Changes

This release has no dependency changes

