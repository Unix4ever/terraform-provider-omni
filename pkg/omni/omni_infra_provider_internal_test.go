// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package omni

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInfraProviderServiceAccountName(t *testing.T) {
	r := &infraProviderResource{}

	assert.Equal(t, "infra-provider:proxmox", r.serviceAccountName("proxmox"))
	assert.Equal(t, "infra-provider:", r.serviceAccountName(""))
}

func TestInfraProviderNamePattern(t *testing.T) {
	for _, tc := range []struct {
		name  string
		valid bool
	}{
		{name: "proxmox", valid: true},
		{name: "a", valid: true},
		{name: "my-provider-2", valid: true},
		{name: "with-dots.invalid", valid: false},
		{name: "UpperCase", valid: false},
		{name: "-leading", valid: false},
		{name: "trailing-", valid: false},
		{name: "", valid: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.valid, infraProviderNamePattern.MatchString(tc.name), "name %q", tc.name)
		})
	}
}
