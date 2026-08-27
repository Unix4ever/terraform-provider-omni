// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package omni_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/siderolabs/terraform-provider-omni/pkg/omni"
)

func TestAccOmniServiceAccountDataSource(t *testing.T) {
	name := acctest.RandomWithPrefix("tf-acc-sa-ds")

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: omni.TestAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServiceAccountDestroy,
		Steps: []resource.TestStep{
			{ // look up the service account created by the resource
				Config: testAccServiceAccountDataSourceConfig(name, "Operator"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.omni_service_account.test", "name", name),
					resource.TestCheckResourceAttr("data.omni_service_account.test", "role", "Operator"),
					resource.TestCheckResourceAttr("data.omni_service_account.test", "public_key_ids.#", "1"),
					// never authenticated, so it has no last active time
					resource.TestCheckNoResourceAttr("data.omni_service_account.test", "last_active"),
					// the data source must resolve to the same account the resource created
					resource.TestCheckResourceAttrPair(
						"data.omni_service_account.test", "id",
						"omni_service_account.test", "id",
					),
					resource.TestCheckResourceAttrPair(
						"data.omni_service_account.test", "role",
						"omni_service_account.test", "role",
					),
					resource.TestCheckResourceAttrPair(
						"data.omni_service_account.test", "expiration",
						"omni_service_account.test", "expiration",
					),
					// the set is unordered, so the resource's key is matched against any element
					resource.TestCheckTypeSetElemAttrPair(
						"data.omni_service_account.test", "public_key_ids.*",
						"omni_service_account.test", "public_key_id",
					),
				),
			},
		},
	})
}

// TestAccOmniServiceAccountDataSourceMissing asserts that looking up an account that does not exist
// is an error rather than an empty result.
func TestAccOmniServiceAccountDataSourceMissing(t *testing.T) {
	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: omni.TestAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
provider "omni" {
  insecure_skip_tls_verify = true
}

data "omni_service_account" "missing" {
  name = "tf-acc-sa-does-not-exist"
}
`,
				ExpectError: regexp.MustCompile(`Omni\s+service\s+account\s+not\s+found`),
			},
		},
	})
}

func testAccServiceAccountDataSourceConfig(name, role string) string {
	return fmt.Sprintf(`
provider "omni" {
  insecure_skip_tls_verify = true
}

resource "omni_service_account" "test" {
  name = %q
  role = %q
}

data "omni_service_account" "test" {
  name = omni_service_account.test.name
}
`, name, role)
}
