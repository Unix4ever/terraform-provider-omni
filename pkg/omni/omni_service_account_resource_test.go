// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package omni_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/siderolabs/terraform-provider-omni/pkg/omni"
)

func TestAccOmniServiceAccountResource(t *testing.T) {
	name := acctest.RandomWithPrefix("tf-acc-sa")

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: omni.TestAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServiceAccountDestroy,
		Steps: []resource.TestStep{
			{ // create with an explicit role
				Config: testAccServiceAccountConfig(name, `  role = "Reader"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("omni_service_account.test", "name", name),
					resource.TestCheckResourceAttr("omni_service_account.test", "role", "Reader"),
					resource.TestCheckResourceAttr("omni_service_account.test", "ttl", "8760h"),
					resource.TestCheckResourceAttr("omni_service_account.test", "id", name+"@serviceaccount.omni.sidero.dev"),
					resource.TestCheckResourceAttrSet("omni_service_account.test", "public_key_id"),
					resource.TestCheckResourceAttrSet("omni_service_account.test", "expiration"),
					testAccCheckServiceAccountKeyUsable("omni_service_account.test", name),
					testAccCheckServiceAccountRole(name, "Reader"),
				),
			},
			{ // the key is not recoverable, so it is absent after an import
				ResourceName:                         "omni_service_account.test",
				ImportState:                          true,
				ImportStateId:                        name,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
				// ttl is an input Omni does not store, and key is write-once at create time.
				ImportStateVerifyIgnore: []string{"ttl", "key"},
			},
		},
	})
}

// TestAccOmniServiceAccountResourceInheritedRole covers the unset-role path, where the account
// inherits the role of the identity Terraform authenticates as and that role is read back.
func TestAccOmniServiceAccountResourceInheritedRole(t *testing.T) {
	name := acctest.RandomWithPrefix("tf-acc-sa-inherit")

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: omni.TestAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServiceAccountDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccServiceAccountConfig(name, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					// The acceptance suite authenticates as an Admin service account, so that is what
					// an unset role resolves to.
					resource.TestCheckResourceAttr("omni_service_account.test", "role", "Admin"),
					testAccCheckServiceAccountRole(name, "Admin"),
				),
			},
		},
	})
}

// TestAccOmniServiceAccountResourceRoleForcesReplacement asserts that a role change replaces the
// account: Omni has no API to change one in place.
func TestAccOmniServiceAccountResourceRoleForcesReplacement(t *testing.T) {
	name := acctest.RandomWithPrefix("tf-acc-sa-replace")

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: omni.TestAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServiceAccountDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccServiceAccountConfig(name, `  role = "Reader"`),
				Check:  testAccCheckServiceAccountRole(name, "Reader"),
			},
			{
				Config: testAccServiceAccountConfig(name, `  role = "Operator"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("omni_service_account.test", "role", "Operator"),
					testAccCheckServiceAccountRole(name, "Operator"),
				),
			},
		},
	})
}

// TestAccOmniServiceAccountResourceRenew asserts that changing renew_trigger or ttl issues a fresh
// key without replacing the account: the identity and public key ID must survive, the key must not.
func TestAccOmniServiceAccountResourceRenew(t *testing.T) {
	name := acctest.RandomWithPrefix("tf-acc-sa-renew")

	var firstKey, firstKeyID, secondKeyID string

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: omni.TestAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServiceAccountDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccServiceAccountConfig(name, "  role          = \"Reader\"\n  renew_trigger = \"first\"\n"),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccRecordServiceAccountAttr("omni_service_account.test", "key", &firstKey),
					testAccRecordServiceAccountAttr("omni_service_account.test", "public_key_id", &firstKeyID),
				),
			},
			{ // a changed trigger renews rather than replaces
				Config: testAccServiceAccountConfig(name, "  role          = \"Reader\"\n  renew_trigger = \"second\"\n"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("omni_service_account.test", "id", name+"@serviceaccount.omni.sidero.dev"),
					resource.TestCheckResourceAttr("omni_service_account.test", "role", "Reader"),
					testAccCheckServiceAccountAttrChanged("omni_service_account.test", "key", &firstKey),
					testAccCheckServiceAccountAttrChanged("omni_service_account.test", "public_key_id", &firstKeyID),
					testAccRecordServiceAccountAttr("omni_service_account.test", "public_key_id", &secondKeyID),
					// renewing adds a key, so the account now carries both
					testAccCheckServiceAccountKeyCount(name, 2),
				),
			},
			{ // a changed ttl renews too
				Config: testAccServiceAccountConfig(name, "  role          = \"Reader\"\n  renew_trigger = \"second\"\n  ttl           = \"720h\"\n"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("omni_service_account.test", "ttl", "720h"),
					testAccCheckServiceAccountAttrChanged("omni_service_account.test", "public_key_id", &secondKeyID),
					testAccCheckServiceAccountKeyCount(name, 3),
				),
			},
		},
	})
}

// testAccRecordServiceAccountAttr stores an attribute value so a later step can compare against it.
func testAccRecordServiceAccountAttr(resourceName, attr string, into *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("resource %q not found in state", resourceName)
		}

		value := rs.Primary.Attributes[attr]
		if value == "" {
			return fmt.Errorf("attribute %q of %q is empty", attr, resourceName)
		}

		*into = value

		return nil
	}
}

// testAccCheckServiceAccountAttrChanged asserts an attribute differs from a previously recorded value.
func testAccCheckServiceAccountAttrChanged(resourceName, attr string, previous *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("resource %q not found in state", resourceName)
		}

		value := rs.Primary.Attributes[attr]
		if value == "" {
			return fmt.Errorf("attribute %q of %q is empty", attr, resourceName)
		}

		if value == *previous {
			return fmt.Errorf("attribute %q of %q did not change across the renew", attr, resourceName)
		}

		return nil
	}
}

// testAccCheckServiceAccountKeyCount asserts, via the live Omni API, how many public keys the account
// carries; renewing adds one rather than replacing.
func testAccCheckServiceAccountKeyCount(name string, want int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client, err := newTestClient()
		if err != nil {
			return err
		}
		defer client.Close() //nolint:errcheck

		serviceAccounts, err := client.Management().ListServiceAccounts(context.Background())
		if err != nil {
			return fmt.Errorf("failed to list service accounts: %w", err)
		}

		for _, account := range serviceAccounts {
			if account.GetName() != name {
				continue
			}

			if got := len(account.GetPgpPublicKeys()); got != want {
				return fmt.Errorf("service account %q has %d public keys, want %d", name, got, want)
			}

			return nil
		}

		return fmt.Errorf("service account %q does not exist", name)
	}
}

// TestAccOmniServiceAccountResourceRejectsInfraProvider asserts that infra providers cannot be
// created through this resource: they need an InfraProvider resource alongside the service account,
// and Omni's cleanup ties the two together.
func TestAccOmniServiceAccountResourceRejectsInfraProvider(t *testing.T) {
	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: omni.TestAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{ // by name prefix
				Config:      testAccServiceAccountConfig("infra-provider:tf-acc-sa-infra", ""),
				ExpectError: regexp.MustCompile(`omnictl\s+infraprovider\s+create`),
			},
			{ // by role
				Config:      testAccServiceAccountConfig("tf-acc-sa-not-infra", `  role = "InfraProvider"`),
				ExpectError: regexp.MustCompile(`omnictl\s+infraprovider\s+create`),
			},
			{ // on import, so an infra provider cannot enter state by the back door either
				Config:        testAccServiceAccountConfig("tf-acc-sa-infra-import", ""),
				ResourceName:  "omni_service_account.test",
				ImportState:   true,
				ImportStateId: "infra-provider:tf-acc-sa-infra",
				ExpectError:   regexp.MustCompile(`omnictl\s+infraprovider\s+create`),
			},
		},
	})
}

// TestAccOmniServiceAccountResourceInvalidTTL asserts that a TTL beyond Omni's one-year maximum is
// rejected at plan time rather than by the server.
func TestAccOmniServiceAccountResourceInvalidTTL(t *testing.T) {
	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: omni.TestAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      testAccServiceAccountConfig("tf-acc-sa-ttl", "  ttl = \"9000h\"\n"),
				ExpectError: regexp.MustCompile(`must\s+not\s+exceed`),
			},
			{
				Config:      testAccServiceAccountConfig("tf-acc-sa-ttl", "  ttl = \"forever\"\n"),
				ExpectError: regexp.MustCompile(`is\s+not\s+a\s+valid\s+duration`),
			},
		},
	})
}

func testAccServiceAccountConfig(name, extra string) string {
	return fmt.Sprintf(`
provider "omni" {
  insecure_skip_tls_verify = true
}

resource "omni_service_account" "test" {
  name = %q
%s
}
`, name, extra)
}

// testAccCheckServiceAccountKeyUsable asserts that `key` really is an OMNI_SERVICE_ACCOUNT_KEY: it
// decodes as the base64 JSON envelope carrying the account name and an armored private key.
func testAccCheckServiceAccountKeyUsable(resourceName, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("resource %q not found in state", resourceName)
		}

		encoded := rs.Primary.Attributes["key"]
		if encoded == "" {
			return fmt.Errorf("service account %q has an empty key", name)
		}

		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return fmt.Errorf("service account key is not valid base64: %w", err)
		}

		var envelope struct {
			Name   string `json:"name"`
			PGPKey string `json:"pgp_key"`
		}

		if err = json.Unmarshal(decoded, &envelope); err != nil {
			return fmt.Errorf("service account key is not valid JSON: %w", err)
		}

		if envelope.Name != name {
			return fmt.Errorf("service account key carries name %q, want %q", envelope.Name, name)
		}

		if envelope.PGPKey == "" {
			return fmt.Errorf("service account key carries no PGP key")
		}

		return nil
	}
}

// testAccCheckServiceAccountRole asserts, via the live Omni API, that the account exists with the
// expected role.
func testAccCheckServiceAccountRole(name, role string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client, err := newTestClient()
		if err != nil {
			return err
		}
		defer client.Close() //nolint:errcheck

		serviceAccounts, err := client.Management().ListServiceAccounts(context.Background())
		if err != nil {
			return fmt.Errorf("failed to list service accounts: %w", err)
		}

		for _, account := range serviceAccounts {
			if account.GetName() != name {
				continue
			}

			if got := account.GetRole(); got != role {
				return fmt.Errorf("unexpected role for %q: got %q, want %q", name, got, role)
			}

			return nil
		}

		return fmt.Errorf("service account %q does not exist", name)
	}
}

// testAccCheckServiceAccountDestroy asserts, via the live Omni API, that every managed service
// account is gone.
func testAccCheckServiceAccountDestroy(s *terraform.State) error {
	client, err := newTestClient()
	if err != nil {
		return err
	}
	defer client.Close() //nolint:errcheck

	serviceAccounts, err := client.Management().ListServiceAccounts(context.Background())
	if err != nil {
		return fmt.Errorf("failed to list service accounts: %w", err)
	}

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "omni_service_account" {
			continue
		}

		name := rs.Primary.Attributes["name"]

		for _, account := range serviceAccounts {
			if account.GetName() == name {
				return fmt.Errorf("service account %q still exists", name)
			}
		}
	}

	return nil
}
