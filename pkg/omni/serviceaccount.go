// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package omni

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/siderolabs/go-api-signature/pkg/pgp"
	"github.com/siderolabs/go-api-signature/pkg/serviceaccount"
	"github.com/siderolabs/omni/client/api/omni/management"
	pkgaccess "github.com/siderolabs/omni/client/pkg/access"
	omniclient "github.com/siderolabs/omni/client/pkg/client"
)

// errServiceAccountNotFound reports a service account that is absent from the listing, so callers
// can treat a vanished account the way COSI-backed resources treat a not-found error.
var errServiceAccountNotFound = errors.New("service account not found")

// serviceAccountMaxTTL mirrors Omni's own ServiceAccountMaxAllowedLifetime: the server rejects a
// public key whose lifetime exceeds a year.
const serviceAccountMaxTTL = 365 * 24 * time.Hour

// serviceAccountDefaultTTL matches the default of `omnictl serviceaccount create --ttl`.
const serviceAccountDefaultTTL = "8760h"

// serviceAccountReadBackTimeout bounds how long a create waits for a freshly created service account
// to turn up in the listing.
const serviceAccountReadBackTimeout = 30 * time.Second

// serviceAccountReadBackInterval is how often the read-back polls the listing while it waits.
const serviceAccountReadBackInterval = time.Second

// createServiceAccountKey generates a PGP key pair for a service account, registers the public half
// with Omni and returns the registered key ID along with the encoded private half.
func createServiceAccountKey(
	ctx context.Context, client *omniclient.Client, name string, ttl time.Duration, accountRole string, useUserRole bool,
) (publicKeyID, encodedKey string, err error) {
	key, err := generateServiceAccountKey(name, ttl)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate the service account key: %w", err)
	}

	armoredPublicKey, err := key.ArmorPublic()
	if err != nil {
		return "", "", fmt.Errorf("failed to armor the service account public key: %w", err)
	}

	if publicKeyID, err = client.Management().CreateServiceAccount(ctx, name, armoredPublicKey, accountRole, useUserRole); err != nil {
		return "", "", err
	}

	if encodedKey, err = serviceaccount.Encode(name, key); err != nil {
		return "", "", fmt.Errorf("failed to encode the service account key: %w", err)
	}

	return publicKeyID, encodedKey, nil
}

// renewServiceAccountKey generates a fresh PGP key pair and registers it with an existing service
// account, returning the new key ID and the encoded private half.
//
// Renewing adds a key rather than replacing one: the account keeps its identity and its previously
// issued keys stay valid until they expire, so anything still holding an old key keeps working until
// then.
func renewServiceAccountKey(ctx context.Context, client *omniclient.Client, name string, ttl time.Duration) (publicKeyID, encodedKey string, err error) {
	key, err := generateServiceAccountKey(name, ttl)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate the service account key: %w", err)
	}

	armoredPublicKey, err := key.ArmorPublic()
	if err != nil {
		return "", "", fmt.Errorf("failed to armor the service account public key: %w", err)
	}

	if publicKeyID, err = client.Management().RenewServiceAccount(ctx, name, armoredPublicKey); err != nil {
		return "", "", err
	}

	if encodedKey, err = serviceaccount.Encode(name, key); err != nil {
		return "", "", fmt.Errorf("failed to encode the service account key: %w", err)
	}

	return publicKeyID, encodedKey, nil
}

// generateServiceAccountKey generates the PGP key pair backing a service account, matching what
// `omnictl serviceaccount create` generates.
func generateServiceAccountKey(name string, ttl time.Duration) (*pgp.Key, error) {
	sa := pkgaccess.ParseServiceAccountFromName(name)
	comment := fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)

	return pgp.GenerateKey(sa.BaseName, comment, sa.FullID(), ttl)
}

// lookupServiceAccount returns the service account with the given name.
func lookupServiceAccount(ctx context.Context, client *omniclient.Client, name string) (*management.ListServiceAccountsResponse_ServiceAccount, error) {
	serviceAccounts, err := client.Management().ListServiceAccounts(ctx)
	if err != nil {
		return nil, err
	}

	for _, account := range serviceAccounts {
		if account.GetName() == name {
			return account, nil
		}
	}

	return nil, fmt.Errorf("%w: %q", errServiceAccountNotFound, name)
}

// lookupServiceAccountKeyWithRetry waits for a specific public key to appear on a service account.
//
// Omni assembles the listing from a controller-produced status, so a key registered by a create or a
// renew is not visible the instant the call returns. Waiting for the key rather than merely for the
// account matters on a renew: the account is already there, so a lookup that only waits for the
// account happily returns a listing that still shows the previous key.
func lookupServiceAccountKeyWithRetry(
	ctx context.Context, client *omniclient.Client, name, keyID string,
) (*management.ListServiceAccountsResponse_ServiceAccount, error) {
	ctx, cancel := context.WithTimeout(ctx, serviceAccountReadBackTimeout)
	defer cancel()

	ticker := time.NewTicker(serviceAccountReadBackInterval)
	defer ticker.Stop()

	for {
		account, err := lookupServiceAccount(ctx, client, name)
		if err != nil && !errors.Is(err, errServiceAccountNotFound) {
			return nil, err
		}

		if err == nil {
			if _, ok := serviceAccountKeyExpiration(account, keyID); ok {
				return account, nil
			}
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("public key %q did not appear on service account %q within %s",
				keyID, name, serviceAccountReadBackTimeout)
		case <-ticker.C:
		}
	}
}

// latestServiceAccountKey returns the ID and expiration of the account's longest-lived public key.
//
// A renewed account carries several keys; the one that matters is whichever expires last, since that
// is when the account actually stops working. Returning both from one place keeps a resource's key
// ID and expiration describing the same key rather than a mix.
func latestServiceAccountKey(account *management.ListServiceAccountsResponse_ServiceAccount) (string, time.Time) {
	var (
		latest      time.Time
		latestKeyID string
	)

	for _, publicKey := range account.GetPgpPublicKeys() {
		if expiration := publicKey.GetExpiration(); expiration != nil && expiration.AsTime().After(latest) {
			latest = expiration.AsTime()
			latestKeyID = publicKey.GetId()
		}
	}

	return latestKeyID, latest
}

// serviceAccountKeyExpiration returns the expiration of a specific public key of the account.
//
// A renewed account carries several keys, so the caller that knows which key it holds asks for that
// one by ID rather than guessing; latestServiceAccountKey is the fallback for a resource that does
// not hold a key at all, such as one that was imported.
func serviceAccountKeyExpiration(account *management.ListServiceAccountsResponse_ServiceAccount, keyID string) (time.Time, bool) {
	for _, publicKey := range account.GetPgpPublicKeys() {
		if publicKey.GetId() != keyID {
			continue
		}

		if expiration := publicKey.GetExpiration(); expiration != nil {
			return expiration.AsTime(), true
		}

		return time.Time{}, false
	}

	return time.Time{}, false
}

// parseServiceAccountTTL parses and range-checks a `ttl` attribute.
func parseServiceAccountTTL(value string) (time.Duration, error) {
	ttl, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%q is not a valid duration: %w", value, err)
	}

	if ttl <= 0 {
		return 0, fmt.Errorf("ttl must be positive, got %q", value)
	}

	if ttl > serviceAccountMaxTTL {
		return 0, fmt.Errorf("ttl must not exceed %s (Omni's maximum service account key lifetime), got %q", serviceAccountMaxTTL, value)
	}

	return ttl, nil
}
