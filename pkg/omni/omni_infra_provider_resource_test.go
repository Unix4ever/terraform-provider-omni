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

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/siderolabs/omni/client/pkg/omni/resources/infra"

	"github.com/siderolabs/terraform-provider-omni/pkg/omni"
)

func TestAccOmniInfraProviderResource(t *testing.T) {
	name := acctest.RandomWithPrefix("tf-acc-ip")

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: omni.TestAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckInfraProviderDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccInfraProviderConfig(name, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("omni_infra_provider.test", "name", name),
					resource.TestCheckResourceAttr("omni_infra_provider.test", "ttl", "8760h"),
					resource.TestCheckResourceAttr("omni_infra_provider.test", "service_account_id", name+"@infra-provider.serviceaccount.omni.sidero.dev"),
					resource.TestCheckResourceAttrSet("omni_infra_provider.test", "public_key_id"),
					testAccCheckInfraProviderKeyUsable("omni_infra_provider.test", name),
				),
			},
			{ // the key is not recoverable, so it is absent after an import
				ResourceName:                         "omni_infra_provider.test",
				ImportState:                          true,
				ImportStateId:                        name,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
				// ttl is an input Omni does not store, key is write-once at create time, and the
				// public key ID cannot be read back for an infra provider (its service account is
				// not listable), so all three are absent after an import.
				ImportStateVerifyIgnore: []string{"ttl", "key", "public_key_id"},
			},
		},
	})
}

func TestAccOmniInfraProviderResourceRenew(t *testing.T) {
	name := acctest.RandomWithPrefix("tf-acc-ip-renew")

	var firstKey, firstKeyID, secondKeyID string

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: omni.TestAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckInfraProviderDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccInfraProviderConfig(name, "  renew_trigger = \"first\"\n"),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccRecordInfraProviderAttr("omni_infra_provider.test", "key", &firstKey),
					testAccRecordInfraProviderAttr("omni_infra_provider.test", "public_key_id", &firstKeyID),
				),
			},
			{ // a changed trigger renews rather than replaces
				Config: testAccInfraProviderConfig(name, "  renew_trigger = \"second\"\n"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("omni_infra_provider.test", "service_account_id", name+"@infra-provider.serviceaccount.omni.sidero.dev"),
					testAccCheckInfraProviderAttrChanged("omni_infra_provider.test", "key", &firstKey),
					testAccCheckInfraProviderAttrChanged("omni_infra_provider.test", "public_key_id", &firstKeyID),
					testAccRecordInfraProviderAttr("omni_infra_provider.test", "public_key_id", &secondKeyID),
				),
			},
			{ // a changed ttl renews too
				Config: testAccInfraProviderConfig(name, "  renew_trigger = \"second\"\n  ttl           = \"720h\"\n"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("omni_infra_provider.test", "ttl", "720h"),
					testAccCheckInfraProviderAttrChanged("omni_infra_provider.test", "public_key_id", &secondKeyID),
				),
			},
		},
	})
}

func TestAccOmniInfraProviderResourceInvalidTTL(t *testing.T) {
	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: omni.TestAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      testAccInfraProviderConfig("tf-acc-ip-ttl", "  ttl = \"9000h\"\n"),
				ExpectError: regexp.MustCompile(`must\s+not\s+exceed`),
			},
			{
				Config:      testAccInfraProviderConfig("tf-acc-ip-ttl", "  ttl = \"forever\"\n"),
				ExpectError: regexp.MustCompile(`is\s+not\s+a\s+valid\s+duration`),
			},
		},
	})
}

func TestAccOmniInfraProviderResourceInvalidName(t *testing.T) {
	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: omni.TestAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      testAccInfraProviderConfig("Bad.Name", ""),
				ExpectError: regexp.MustCompile(`DNS-1123\s+label`),
			},
			{
				Config:      testAccInfraProviderConfig("Uppercase", ""),
				ExpectError: regexp.MustCompile(`DNS-1123\s+label`),
			},
			{
				Config:      testAccInfraProviderConfig("-leading", ""),
				ExpectError: regexp.MustCompile(`DNS-1123\s+label`),
			},
		},
	})
}

func testAccInfraProviderConfig(name, extra string) string {
	return fmt.Sprintf(`
provider "omni" {
  insecure_skip_tls_verify = true
}

resource "omni_infra_provider" "test" {
  name = %q
%s
}
`, name, extra)
}

// testAccRecordInfraProviderAttr stores an attribute value so a later step can compare against it.
func testAccRecordInfraProviderAttr(resourceName, attr string, into *string) resource.TestCheckFunc {
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

// testAccCheckInfraProviderAttrChanged asserts an attribute differs from a previously recorded value.
func testAccCheckInfraProviderAttrChanged(resourceName, attr string, previous *string) resource.TestCheckFunc {
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

// testAccCheckInfraProviderKeyUsable asserts that `key` really is an OMNI_SERVICE_ACCOUNT_KEY.
func testAccCheckInfraProviderKeyUsable(resourceName, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("resource %q not found in state", resourceName)
		}

		encoded := rs.Primary.Attributes["key"]
		if encoded == "" {
			return fmt.Errorf("infra provider %q has an empty key", name)
		}

		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return fmt.Errorf("infra provider key is not valid base64: %w", err)
		}

		var envelope struct {
			Name   string `json:"name"`
			PGPKey string `json:"pgp_key"`
		}

		if err = json.Unmarshal(decoded, &envelope); err != nil {
			return fmt.Errorf("infra provider key is not valid JSON: %w", err)
		}

		if envelope.Name != "infra-provider:"+name {
			return fmt.Errorf("infra provider key carries name %q, want %q", envelope.Name, "infra-provider:"+name)
		}

		if envelope.PGPKey == "" {
			return fmt.Errorf("infra provider key carries no PGP key")
		}

		return nil
	}
}

// testAccCheckInfraProviderDestroy asserts, via the live Omni API, that every managed infra provider
// is gone. The tied service account cannot be verified here: an infra provider's service account is
// not surfaced by ListServiceAccounts, so its cleanup (done by Omni when the provider is destroyed)
// is not observable through the management API.
func testAccCheckInfraProviderDestroy(s *terraform.State) error {
	client, err := newTestClient()
	if err != nil {
		return err
	}
	defer client.Close() //nolint:errcheck

	// The single-resource Get path is blocked for the infra-provider namespace, so existence is
	// checked by listing and filtering, the same way Read does.
	providers, err := safe.StateList[*infra.Provider](context.Background(), client.Omni().State(), infra.NewProvider("").Metadata())
	if err != nil {
		return fmt.Errorf("failed to list infra providers: %w", err)
	}

	existing := map[string]struct{}{}

	for provider := range providers.All() {
		existing[provider.Metadata().ID()] = struct{}{}
	}

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "omni_infra_provider" {
			continue
		}

		name := rs.Primary.Attributes["name"]

		if _, ok := existing[name]; ok {
			return fmt.Errorf("infra provider %q still exists", name)
		}
	}

	return nil
}
