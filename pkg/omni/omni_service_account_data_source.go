// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package omni

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pkgaccess "github.com/siderolabs/omni/client/pkg/access"
)

// Ensure the data source satisfies the framework interfaces.
var (
	_ datasource.DataSource              = (*serviceAccountDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*serviceAccountDataSource)(nil)
)

// serviceAccountDataSourceModel maps the omni_service_account data source schema.
type serviceAccountDataSourceModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	Role         types.String `tfsdk:"role"`
	Expiration   types.String `tfsdk:"expiration"`
	LastActive   types.String `tfsdk:"last_active"`
	PublicKeyIDs types.Set    `tfsdk:"public_key_ids"`
}

// serviceAccountDataSource implements the omni_service_account data source.
type serviceAccountDataSource struct {
	data *providerData
}

// NewServiceAccountDataSource returns a new omni_service_account data source.
func NewServiceAccountDataSource() datasource.DataSource {
	return &serviceAccountDataSource{}
}

// Metadata implements datasource.DataSource.
func (d *serviceAccountDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_account"
}

// Schema implements datasource.DataSource.
func (d *serviceAccountDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Looks up an existing Omni service account by name. The service account key is not returned: Omni " +
			"never stores the private half, so it is only available from the `omni_service_account` resource that " +
			"created it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "The full identity ID of the service account, e.g. `automation@serviceaccount.omni.sidero.dev`.",
			},
			"name": schema.StringAttribute{
				Required: true,
				Description: "The name of the service account to look up. Infra provider service accounts keep their " +
					"`infra-provider:` prefix in the name, and can be read here even though they cannot be managed by " +
					"the `omni_service_account` resource.",
			},
			"role": schema.StringAttribute{
				Computed:    true,
				Description: "The role of the service account.",
			},
			"expiration": schema.StringAttribute{
				Computed: true,
				Description: "When the service account's longest-lived key expires, in RFC 3339 format. Null if it has no " +
					"keys with an expiration.",
			},
			"last_active": schema.StringAttribute{
				Computed: true,
				Description: "When the service account last authenticated, in RFC 3339 format. Null if it has never " +
					"authenticated.",
			},
			// A set rather than a list: Omni gives no meaningful order to the keys, so an index into
			// them would mean nothing and the order could change between reads.
			"public_key_ids": schema.SetAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "The IDs of the registered PGP public keys, unordered. A renewed service account has more than one.",
			},
		},
	}
}

// Configure implements datasource.DataSourceWithConfigure.
func (d *serviceAccountDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = providerDataFromResource(req.ProviderData, &resp.Diagnostics)
}

// Read implements datasource.DataSource.
func (d *serviceAccountDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config serviceAccountDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)

	if resp.Diagnostics.HasError() {
		return
	}

	name := config.Name.ValueString()

	account, err := lookupServiceAccount(ctx, d.data.client, name)
	if err != nil {
		if errors.Is(err, errServiceAccountNotFound) {
			resp.Diagnostics.AddError(
				"Omni service account not found",
				fmt.Sprintf("No service account named %q exists.", name),
			)

			return
		}

		errToDiag(&resp.Diagnostics, "Failed to list Omni service accounts", err)

		return
	}

	publicKeyIDs := make([]string, 0, len(account.GetPgpPublicKeys()))
	for _, publicKey := range account.GetPgpPublicKeys() {
		publicKeyIDs = append(publicKeyIDs, publicKey.GetId())
	}

	ids, diags := types.SetValueFrom(ctx, types.StringType, publicKeyIDs)
	resp.Diagnostics.Append(diags...)

	if resp.Diagnostics.HasError() {
		return
	}

	config.ID = types.StringValue(pkgaccess.ParseServiceAccountFromName(name).FullID())
	config.Role = types.StringValue(account.GetRole())
	config.LastActive = optionalString(account.GetLastActive())
	config.PublicKeyIDs = ids

	if _, expiration := latestServiceAccountKey(account); expiration.IsZero() {
		config.Expiration = types.StringNull()
	} else {
		config.Expiration = types.StringValue(expiration.UTC().Format(time.RFC3339))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
