// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package omni

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/cosi-project/runtime/pkg/safe"
	cosistate "github.com/cosi-project/runtime/pkg/state"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pkgaccess "github.com/siderolabs/omni/client/pkg/access"
	"github.com/siderolabs/omni/client/pkg/access/role"
	"github.com/siderolabs/omni/client/pkg/omni/resources/infra"
)

// infraProviderNamePattern matches an Omni InfraProvider ID, which must be a DNS-1123 label:
// lowercase letters, digits and hyphens, starting and ending with an alphanumeric, at most 63
// characters. Omni rejects anything else at create time, so rejecting it here surfaces the error
// during planning instead of apply.
var infraProviderNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// Ensure the resource satisfies the framework interfaces.
var (
	_ resource.Resource                   = (*infraProviderResource)(nil)
	_ resource.ResourceWithConfigure      = (*infraProviderResource)(nil)
	_ resource.ResourceWithImportState    = (*infraProviderResource)(nil)
	_ resource.ResourceWithValidateConfig = (*infraProviderResource)(nil)
)

// infraProviderResourceModel maps the omni_infra_provider resource schema.
type infraProviderResourceModel struct {
	Name             types.String `tfsdk:"name"`
	TTL              types.String `tfsdk:"ttl"`
	RenewTrigger     types.String `tfsdk:"renew_trigger"`
	ServiceAccountID types.String `tfsdk:"service_account_id"`
	Key              types.String `tfsdk:"key"`
	PublicKeyID      types.String `tfsdk:"public_key_id"`
}

// infraProviderResource implements the omni_infra_provider resource.
//
// An infra provider in Omni is an InfraProvider resource plus a service account named with the
// "infra-provider:" prefix and the InfraProvider role. Omni's cleanup controller removes that
// service account when the InfraProvider resource is destroyed, so this resource creates and
// destroys the two together.
//
// Unlike a regular service account, an infra provider's service account is not surfaced by Omni's
// ListServiceAccounts API, so its expiration cannot be read back. The `key` is only issued once,
// at create or renew time; both are reflected here without a listing read-back.
type infraProviderResource struct {
	data *providerData
}

// NewInfraProviderResource returns a new omni_infra_provider resource.
func NewInfraProviderResource() resource.Resource {
	return &infraProviderResource{}
}

// Metadata implements resource.Resource.
func (r *infraProviderResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_infra_provider"
}

// Schema implements resource.Resource.
func (r *infraProviderResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an Omni infrastructure provider: the InfraProvider resource a machine class's " +
			"`auto_provision` block points at, plus the service account that the provider authenticates to Omni as.\n\n" +
			"The provider generates the PGP key pair, registers the public half with Omni under the role `InfraProvider` " +
			"and exposes the encoded private half as `key`, which is what `OMNI_SERVICE_ACCOUNT_KEY` expects. Omni never " +
			"returns that key again, so it lives only in Terraform state: treat the state as a secret, and note that an " +
			"imported infra provider has a null `key` because the private half cannot be recovered.\n\n" +
			"`name` is immutable, since Omni has no API to change it in place. The key is rotated by renewing rather than " +
			"replacing: change `ttl`, or change `renew_trigger`, and Omni issues a fresh key while the provider keeps its " +
			"identity. Previously issued keys stay valid until they expire.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required: true,
				Description: "The infra provider name, e.g. `proxmox`. It is referenced by machine class `auto_provision` " +
					"blocks via `provider_id`, and it prefixes the service account as `infra-provider:<name>`. It must be " +
					"a DNS-1123 label (lowercase letters, digits and hyphens, starting and ending with an alphanumeric, " +
					"at most 63 characters), matching Omni's InfraProvider ID constraint. Immutable.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 63),
					stringvalidator.RegexMatches(infraProviderNamePattern, "must be a DNS-1123 label: lowercase letters, digits and hyphens, starting and ending with a letter or digit"),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"ttl": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(serviceAccountDefaultTTL),
				Description: "Lifetime of the generated key, as a Go duration (e.g. `720h`). Omni caps it at one year " +
					"(`8760h`), which is also the default. Changing it renews the provider with a fresh key of the new " +
					"lifetime rather than replacing it.",
			},
			"renew_trigger": schema.StringAttribute{
				Optional: true,
				Description: "An arbitrary value that renews the key whenever it changes. Use it to rotate on a schedule " +
					"or on demand without replacing the infra provider, e.g. by feeding it a `time_rotating` resource's " +
					"`rfc3339`. Do not use `timestamp()`, which changes on every plan and would renew on every apply.",
			},
			"service_account_id": schema.StringAttribute{
				Computed: true,
				Description: "The full identity ID of the infra provider's service account, " +
					"e.g. `proxmox@infra-provider.serviceaccount.omni.sidero.dev`.",
			},
			"public_key_id": schema.StringAttribute{
				Computed: true,
				Description: "The ID of the PGP public key backing `key`. A renewed provider keeps its older keys until " +
					"they expire; this always identifies the current one.",
			},
			"key": schema.StringAttribute{
				Computed:  true,
				Sensitive: true,
				Description: "The base64-encoded service account key, the value `OMNI_SERVICE_ACCOUNT_KEY` and the " +
					"provider's `service_account_key` argument take. Omni does not store the private half, so this is " +
					"null on an imported resource, and a renew replaces it with the newly issued key.",
			},
		},
	}
}

// ValidateConfig implements resource.ResourceWithValidateConfig.
func (r *infraProviderResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config infraProviderResourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)

	if resp.Diagnostics.HasError() {
		return
	}

	if !config.TTL.IsNull() && !config.TTL.IsUnknown() {
		if _, err := parseServiceAccountTTL(config.TTL.ValueString()); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("ttl"), "Invalid TTL", err.Error())
		}
	}
}

// Configure implements resource.ResourceWithConfigure.
func (r *infraProviderResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = providerDataFromResource(req.ProviderData, &resp.Diagnostics)
}

// serviceAccountName returns the name of the service account backing an infra provider.
func (r *infraProviderResource) serviceAccountName(name string) string {
	return "infra-provider:" + name
}

// rollbackProvider best-effort destroys a provider created earlier in a failed operation so Omni
// does not keep a provider nothing owns. It returns a non-nil error when the teardown itself fails
// (and the provider is not already gone), so the caller can surface the resulting orphan rather than
// silently leak it.
func rollbackProvider(ctx context.Context, st cosistate.State, provider *infra.Provider) error {
	if err := st.TeardownAndDestroy(ctx, provider.Metadata()); err != nil && !cosistate.IsNotFoundError(err) {
		return err
	}

	return nil
}

// Create implements resource.Resource.
func (r *infraProviderResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan infraProviderResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)

	if resp.Diagnostics.HasError() {
		return
	}

	name := plan.Name.ValueString()

	ttl, err := parseServiceAccountTTL(plan.TTL.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("ttl"), "Invalid TTL", err.Error())

		return
	}

	// Create the InfraProvider resource first. If registering the service account fails below, the
	// provider is torn down again so Omni does not keep a provider nothing owns.
	provider := infra.NewProvider(name)

	if err = r.data.state.Create(ctx, provider); err != nil {
		errToDiag(&resp.Diagnostics, "Failed to create Omni infra provider", err)

		return
	}

	publicKeyID, encodedKey, err := createServiceAccountKey(ctx, r.data.client, r.serviceAccountName(name), ttl, string(role.InfraProvider), false)
	if err != nil {
		if teardownErr := rollbackProvider(ctx, r.data.state, provider); teardownErr != nil {
			err = errors.Join(err, fmt.Errorf("failed to roll back the infra provider: %w", teardownErr))
		}

		errToDiag(&resp.Diagnostics, "Failed to create the infra provider service account", err)

		return
	}

	plan.ServiceAccountID = types.StringValue(pkgaccess.ParseServiceAccountFromName(r.serviceAccountName(name)).FullID())
	plan.PublicKeyID = types.StringValue(publicKeyID)
	plan.Key = types.StringValue(encodedKey)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read implements resource.Resource.
func (r *infraProviderResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state infraProviderResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}

	name := state.Name.ValueString()

	// The COSI state Get API doesn't work for the infra-provider namespace through
	// the gRPC client (the single-resource Get path is blocked), but List does work.
	// Use List + filter to check existence, then trust the existing state for values
	// (the key cannot be recovered from Omni once issued).
	providers, listErr := safe.StateList[*infra.Provider](ctx, r.data.state, infra.NewProvider("").Metadata())
	if listErr != nil {
		errToDiag(&resp.Diagnostics, "Failed to list Omni infra providers", listErr)

		return
	}

	found := false

	for provider := range providers.All() {
		if provider.Metadata().ID() == name {
			found = true

			break
		}
	}

	if !found {
		resp.State.RemoveResource(ctx)

		return
	}

	// The service account ID is deterministic from the name, so it is derived on every read (an
	// imported resource only has `name`). The service account itself is not listable via the
	// management API, so there is nothing else to refresh; the key and public key ID already live
	// in state.
	state.ServiceAccountID = types.StringValue(pkgaccess.ParseServiceAccountFromName(r.serviceAccountName(name)).FullID())

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update implements resource.Resource.
//
// name forces replacement, so the only changes that reach here are a new ttl or a changed
// renew_trigger, both of which mean "issue a fresh key". Omni renews by registering an additional
// key, so the provider keeps its identity and its older keys stay valid until they expire.
func (r *infraProviderResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan infraProviderResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)

	if resp.Diagnostics.HasError() {
		return
	}

	ttl, err := parseServiceAccountTTL(plan.TTL.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("ttl"), "Invalid TTL", err.Error())

		return
	}

	name := plan.Name.ValueString()
	serviceAccountName := r.serviceAccountName(name)

	publicKeyID, encodedKey, err := renewServiceAccountKey(ctx, r.data.client, serviceAccountName, ttl)
	if err != nil {
		errToDiag(&resp.Diagnostics, "Failed to renew the infra provider service account key", err)

		return
	}

	plan.ServiceAccountID = types.StringValue(pkgaccess.ParseServiceAccountFromName(serviceAccountName).FullID())
	plan.PublicKeyID = types.StringValue(publicKeyID)
	plan.Key = types.StringValue(encodedKey)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete implements resource.Resource.
//
// Destroying the InfraProvider resource is enough: Omni's cleanup controller removes the tied
// service account.
func (r *infraProviderResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state infraProviderResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}

	provider := infra.NewProvider(state.Name.ValueString())

	if err := r.data.state.TeardownAndDestroy(ctx, provider.Metadata()); err != nil {
		if cosistate.IsNotFoundError(err) {
			return
		}

		errToDiag(&resp.Diagnostics, "Failed to destroy Omni infra provider", err)

		return
	}
}

// ImportState implements resource.ResourceWithImportState. Infra providers are imported by name.
//
// The private key is not recoverable, so `key` stays null on an imported resource.
func (r *infraProviderResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}
