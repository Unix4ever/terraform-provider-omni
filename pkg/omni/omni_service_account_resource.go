// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package omni

import (
	"context"
	"errors"
	"time"

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
	"github.com/siderolabs/omni/client/api/omni/management"
	pkgaccess "github.com/siderolabs/omni/client/pkg/access"
	"github.com/siderolabs/omni/client/pkg/access/role"
)

// Ensure the resource satisfies the framework interfaces.
var (
	_ resource.Resource                   = (*serviceAccountResource)(nil)
	_ resource.ResourceWithConfigure      = (*serviceAccountResource)(nil)
	_ resource.ResourceWithImportState    = (*serviceAccountResource)(nil)
	_ resource.ResourceWithValidateConfig = (*serviceAccountResource)(nil)
)

// serviceAccountRoleNames are the roles accepted by the `role` attribute.
//
// InfraProvider is deliberately absent: an infra provider is more than a service account (it also
// needs an InfraProvider resource, and Omni ties the two together for cleanup), so it is managed by
// omni_infra_provider instead.
var serviceAccountRoleNames = []string{
	string(role.None),
	string(role.Reader),
	string(role.Auditor),
	string(role.Operator),
	string(role.Admin),
}

// serviceAccountResourceModel maps the omni_service_account resource schema.
type serviceAccountResourceModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	Role         types.String `tfsdk:"role"`
	TTL          types.String `tfsdk:"ttl"`
	RenewTrigger types.String `tfsdk:"renew_trigger"`
	PublicKeyID  types.String `tfsdk:"public_key_id"`
	Expiration   types.String `tfsdk:"expiration"`
	Key          types.String `tfsdk:"key"`
}

// serviceAccountResource implements the omni_service_account resource.
type serviceAccountResource struct {
	data *providerData
}

// NewServiceAccountResource returns a new omni_service_account resource.
func NewServiceAccountResource() resource.Resource {
	return &serviceAccountResource{}
}

// Metadata implements resource.Resource.
func (r *serviceAccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_account"
}

// Schema implements resource.Resource.
func (r *serviceAccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an Omni service account, the non-interactive identity Terraform, CI and infra providers " +
			"authenticate as.\n\n" +
			"The provider generates the PGP key pair, registers the public half with Omni and exposes the encoded " +
			"private half as `key`, which is what `OMNI_SERVICE_ACCOUNT_KEY` expects. Omni never returns that key " +
			"again, so it lives only in Terraform state: treat the state as a secret, and note that an imported " +
			"service account has a null `key` because the private half cannot be recovered.\n\n" +
			"`name` and `role` are immutable, since Omni has no API to change them in place. The key is rotated by " +
			"renewing rather than replacing: change `ttl`, or change `renew_trigger`, and Omni issues a fresh key while " +
			"the account keeps its identity. Previously issued keys stay valid until they expire, so anything still " +
			"holding the old key keeps working until then.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
				Description: "The full identity ID of the service account, e.g. `automation@serviceaccount.omni.sidero.dev`. " +
					"This is what shows up as the identity in audit logs and ACLs.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required: true,
				Description: "The service account name, e.g. `automation`. The `infra-provider:` prefix is rejected: an " +
					"infra provider is an `InfraProvider` resource plus its service account, created and destroyed " +
					"together, so it cannot be managed as a bare service account. Immutable.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"role": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "The role of the service account. One of: `None`, `Reader`, `Auditor`, `Operator`, `Admin`. " +
					"Leave it unset to inherit the role of the identity Terraform itself authenticates as, in which case " +
					"the inherited role is read back into state. `InfraProvider` is not accepted here; see `name`. " +
					"Immutable.",
				Validators: []validator.String{
					stringvalidator.OneOf(serviceAccountRoleNames...),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"ttl": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(serviceAccountDefaultTTL),
				Description: "Lifetime of the generated key, as a Go duration (e.g. `720h`). Omni caps it at one year " +
					"(`8760h`), which is also the default. Changing it renews the account with a fresh key of the new " +
					"lifetime rather than replacing the account.",
			},
			"renew_trigger": schema.StringAttribute{
				Optional: true,
				Description: "An arbitrary value that renews the key whenever it changes. Use it to rotate on a schedule " +
					"or on demand without replacing the service account, e.g. by feeding it a `time_rotating` resource's " +
					"`rfc3339`. Do not use `timestamp()`, which changes on every plan and would renew on every apply.",
			},
			"public_key_id": schema.StringAttribute{
				Computed: true,
				Description: "The ID of the PGP public key backing `key`. A renewed account keeps its older keys until " +
					"they expire; this always identifies the current one.",
			},
			"expiration": schema.StringAttribute{
				Computed: true,
				Description: "When the key in `key` expires, in RFC 3339 format. After this the service account can no " +
					"longer authenticate with it, so it is the signal to renew.",
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
//
// Infra providers are rejected here: they are not merely a service account, so managing one through
// this resource would create a service account Omni cannot tie to a provider.
func (r *serviceAccountResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config serviceAccountResourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)

	if resp.Diagnostics.HasError() {
		return
	}

	if !config.Name.IsNull() && !config.Name.IsUnknown() && isInfraProviderServiceAccountName(config.Name.ValueString()) {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Infra provider service accounts are managed separately", infraProviderServiceAccountMessage)
	}

	// The role is not in the schema's allowed values, so the attribute validator rejects it too; this
	// adds the pointer to the right resource.
	if !config.Role.IsNull() && !config.Role.IsUnknown() && config.Role.ValueString() == string(role.InfraProvider) {
		resp.Diagnostics.AddAttributeError(path.Root("role"), "Infra provider service accounts are managed separately", infraProviderServiceAccountMessage)
	}

	if !config.TTL.IsNull() && !config.TTL.IsUnknown() {
		if _, err := parseServiceAccountTTL(config.TTL.ValueString()); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("ttl"), "Invalid TTL", err.Error())
		}
	}
}

// infraProviderServiceAccountMessage explains why omni_service_account refuses infra providers.
//
// The check is not pedantry: Omni's cleanup controller removes an infra provider's service account
// when the InfraProvider resource is destroyed, so a service account created on its own here would
// be owned by nothing and would outlive anything Terraform could tie it to.
const infraProviderServiceAccountMessage = "An infra provider is an InfraProvider resource plus a service account named with " +
	"the \"infra-provider:\" prefix, created and destroyed together. This resource manages only the service account, which " +
	"would leave an account nothing owns. Infra providers are not managed by this provider yet; create one with " +
	"\"omnictl infraprovider create\"."

// isInfraProviderServiceAccountName reports whether a service account name is an infra provider's.
func isInfraProviderServiceAccountName(name string) bool {
	return pkgaccess.ParseServiceAccountFromName(name).IsInfraProvider
}

// Configure implements resource.ResourceWithConfigure.
func (r *serviceAccountResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = providerDataFromResource(req.ProviderData, &resp.Diagnostics)
}

// Create implements resource.Resource.
func (r *serviceAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serviceAccountResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)

	if resp.Diagnostics.HasError() {
		return
	}

	name := plan.Name.ValueString()

	// Defense in depth: ValidateConfig and ImportState both reject this already, so reaching here
	// means one of them was bypassed. Creating the account anyway would leave one nothing owns.
	if isInfraProviderServiceAccountName(name) {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Infra provider service accounts are managed separately", infraProviderServiceAccountMessage)

		return
	}

	ttl, err := parseServiceAccountTTL(plan.TTL.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("ttl"), "Invalid TTL", err.Error())

		return
	}

	// An unset role means "inherit the role of whoever Terraform is authenticating as", which is
	// exactly what use_user_role does; Omni ignores the role field in that case.
	useUserRole := plan.Role.IsNull() || plan.Role.IsUnknown()

	publicKeyID, encodedKey, err := createServiceAccountKey(ctx, r.data.client, name, ttl, plan.Role.ValueString(), useUserRole)
	if err != nil {
		errToDiag(&resp.Diagnostics, "Failed to create Omni service account", err)

		return
	}

	plan.ID = types.StringValue(pkgaccess.ParseServiceAccountFromName(name).FullID())
	plan.PublicKeyID = types.StringValue(publicKeyID)
	plan.Key = types.StringValue(encodedKey)

	// The role is Computed as well as Optional, so an inherited role has to be resolved to the
	// concrete value the server settled on, and the expiration is only known once the key is
	// registered. Both come from the service account listing, which Omni builds from a
	// controller-produced status, so it lags the call that created the key.
	account, err := lookupServiceAccountKeyWithRetry(ctx, r.data.client, name, publicKeyID)
	if err != nil {
		errToDiag(&resp.Diagnostics, "Failed to read back the created Omni service account", err)

		return
	}

	r.accountToModel(account, &plan)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read implements resource.Resource.
func (r *serviceAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serviceAccountResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}

	account, err := lookupServiceAccount(ctx, r.data.client, state.Name.ValueString())
	if err != nil {
		if errors.Is(err, errServiceAccountNotFound) {
			resp.State.RemoveResource(ctx)

			return
		}

		errToDiag(&resp.Diagnostics, "Failed to read Omni service account", err)

		return
	}

	r.accountToModel(account, &state)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update implements resource.Resource.
//
// name and role force replacement, so the only changes that reach here are a new ttl or a changed
// renew_trigger, both of which mean "issue a fresh key". Omni renews by registering an additional
// key, so the account keeps its identity and its older keys stay valid until they expire.
func (r *serviceAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serviceAccountResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}

	ttl, err := parseServiceAccountTTL(plan.TTL.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("ttl"), "Invalid TTL", err.Error())

		return
	}

	name := plan.Name.ValueString()

	publicKeyID, encodedKey, err := renewServiceAccountKey(ctx, r.data.client, name, ttl)
	if err != nil {
		errToDiag(&resp.Diagnostics, "Failed to renew the Omni service account key", err)

		return
	}

	plan.ID = types.StringValue(pkgaccess.ParseServiceAccountFromName(name).FullID())
	plan.PublicKeyID = types.StringValue(publicKeyID)
	plan.Key = types.StringValue(encodedKey)

	// Wait for the key just issued rather than merely for the account: the account already exists,
	// so a listing fetched too early still shows only the previous key.
	account, err := lookupServiceAccountKeyWithRetry(ctx, r.data.client, name, publicKeyID)
	if err != nil {
		errToDiag(&resp.Diagnostics, "Failed to read back the renewed Omni service account", err)

		return
	}

	r.accountToModel(account, &plan)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete implements resource.Resource.
func (r *serviceAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serviceAccountResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.data.client.Management().DestroyServiceAccount(ctx, state.Name.ValueString()); err != nil {
		if cosistate.IsNotFoundError(err) {
			return
		}

		errToDiag(&resp.Diagnostics, "Failed to destroy Omni service account", err)

		return
	}
}

// ImportState implements resource.ResourceWithImportState. Service accounts are imported by name.
//
// Infra providers are refused here as they are everywhere else in this resource: importing one would
// put a name in state that no subsequent plan can apply. The `omni_service_account` data source
// reads them instead.
//
// The private key is not recoverable, so `key` stays null on an imported resource.
func (r *serviceAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if isInfraProviderServiceAccountName(req.ID) {
		resp.Diagnostics.AddError("Infra provider service accounts are managed separately", infraProviderServiceAccountMessage)

		return
	}

	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

// accountToModel copies the server-owned fields of a service account onto the model.
//
// The expiration must describe the key the model holds rather than whichever key lives longest: a
// renew issued with a shorter ttl than the previous key's remaining life would otherwise report an
// expiration belonging to a key nobody has the private half of. When the model already identifies a
// key the fallback is deliberately not taken, because reporting some other key's ID would claim the
// resource holds a private half it does not have; only a model with no key at all, as after an
// import, adopts the longest-lived one.
func (r *serviceAccountResource) accountToModel(account *management.ListServiceAccountsResponse_ServiceAccount, model *serviceAccountResourceModel) {
	model.ID = types.StringValue(pkgaccess.ParseServiceAccountFromName(model.Name.ValueString()).FullID())
	model.Role = types.StringValue(account.GetRole())

	if keyID := model.PublicKeyID.ValueString(); keyID != "" {
		expiration, ok := serviceAccountKeyExpiration(account, keyID)
		if !ok {
			// The key is gone from the account, so nothing can be said about when it expires. The ID
			// is left in place: it is still the key this resource holds the private half of.
			model.Expiration = types.StringNull()

			return
		}

		model.Expiration = types.StringValue(expiration.UTC().Format(time.RFC3339))

		return
	}

	publicKeyID, expiration := latestServiceAccountKey(account)

	if expiration.IsZero() {
		model.Expiration = types.StringNull()
		model.PublicKeyID = types.StringNull()

		return
	}

	model.Expiration = types.StringValue(expiration.UTC().Format(time.RFC3339))
	model.PublicKeyID = types.StringValue(publicKeyID)
}
