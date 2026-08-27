// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package omni

import (
	"strings"
	"testing"
	"time"

	"github.com/siderolabs/go-api-signature/pkg/pgp"
	"github.com/siderolabs/go-api-signature/pkg/serviceaccount"
	pkgaccess "github.com/siderolabs/omni/client/pkg/access"
)

func TestParseServiceAccountTTL(t *testing.T) {
	for _, tc := range []struct {
		name        string
		in          string
		expectedErr string
		expected    time.Duration
	}{
		{name: "the schema default is accepted", in: serviceAccountDefaultTTL, expected: serviceAccountMaxTTL},
		{name: "a shorter duration is accepted", in: "720h", expected: 720 * time.Hour},
		{name: "surrounding whitespace is tolerated", in: "  24h\n", expected: 24 * time.Hour},
		{name: "exactly the maximum is accepted", in: "8760h", expected: serviceAccountMaxTTL},
		{name: "beyond the maximum is rejected", in: "8761h", expectedErr: "must not exceed"},
		{name: "zero is rejected", in: "0s", expectedErr: "must be positive"},
		{name: "negative is rejected", in: "-1h", expectedErr: "must be positive"},
		{name: "garbage is rejected", in: "forever", expectedErr: "is not a valid duration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseServiceAccountTTL(tc.in)

			if tc.expectedErr != "" {
				if err == nil {
					t.Fatalf("parseServiceAccountTTL(%q) = %v, want error %q", tc.in, got, tc.expectedErr)
				}

				if !strings.Contains(err.Error(), tc.expectedErr) {
					t.Fatalf("parseServiceAccountTTL(%q) error = %q, want it to contain %q", tc.in, err, tc.expectedErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("parseServiceAccountTTL(%q) returned unexpected error: %v", tc.in, err)
			}

			if got != tc.expected {
				t.Fatalf("parseServiceAccountTTL(%q) = %v, want %v", tc.in, got, tc.expected)
			}
		})
	}
}

// TestGenerateServiceAccountKey asserts that the generated key is one Omni will actually accept: it
// is validated with the very function the server runs on the armored public key, and it round-trips
// through the encoding OMNI_SERVICE_ACCOUNT_KEY uses.
func TestGenerateServiceAccountKey(t *testing.T) {
	for _, tc := range []struct {
		name             string
		expectedUsername string
	}{
		{name: "automation", expectedUsername: "automation"},
		// An infra provider name keeps its prefix in the service account key, but the PGP identity is
		// built from the base name and the infra provider suffix.
		{name: "infra-provider:aws-1", expectedUsername: "aws-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ttl := 24 * time.Hour

			key, err := generateServiceAccountKey(tc.name, ttl)
			if err != nil {
				t.Fatalf("generateServiceAccountKey() returned unexpected error: %v", err)
			}

			armoredPublicKey, err := key.ArmorPublic()
			if err != nil {
				t.Fatalf("ArmorPublic() returned unexpected error: %v", err)
			}

			publicKey, err := pkgaccess.ValidatePGPPublicKey([]byte(armoredPublicKey), pgp.WithMaxAllowedLifetime(serviceAccountMaxTTL))
			if err != nil {
				t.Fatalf("Omni would reject the generated public key: %v", err)
			}

			if publicKey.Username != tc.expectedUsername {
				t.Fatalf("key username = %q, want %q", publicKey.Username, tc.expectedUsername)
			}

			if remaining := time.Until(publicKey.Expiration); remaining > ttl || remaining < ttl-time.Minute {
				t.Fatalf("key expires in %v, want approximately %v", remaining, ttl)
			}

			encoded, err := serviceaccount.Encode(tc.name, key)
			if err != nil {
				t.Fatalf("Encode() returned unexpected error: %v", err)
			}

			decoded, err := serviceaccount.Decode(encoded)
			if err != nil {
				t.Fatalf("Decode() returned unexpected error: %v", err)
			}

			if decoded.Name != tc.name {
				t.Fatalf("decoded name = %q, want %q", decoded.Name, tc.name)
			}
		})
	}
}

// TestServiceAccountTTLExceedingMaxIsRejectedByOmni guards the coupling between the ttl ceiling and
// Omni's own limit: a key generated past the maximum must fail the server's validation, which is
// what parseServiceAccountTTL exists to prevent.
func TestServiceAccountTTLExceedingMaxIsRejectedByOmni(t *testing.T) {
	key, err := generateServiceAccountKey("automation", serviceAccountMaxTTL+24*time.Hour)
	if err != nil {
		t.Fatalf("generateServiceAccountKey() returned unexpected error: %v", err)
	}

	armoredPublicKey, err := key.ArmorPublic()
	if err != nil {
		t.Fatalf("ArmorPublic() returned unexpected error: %v", err)
	}

	if _, err = pkgaccess.ValidatePGPPublicKey([]byte(armoredPublicKey), pgp.WithMaxAllowedLifetime(serviceAccountMaxTTL)); err == nil {
		t.Fatal("Omni accepted a key beyond the maximum lifetime; serviceAccountMaxTTL is out of step with the server")
	}
}
