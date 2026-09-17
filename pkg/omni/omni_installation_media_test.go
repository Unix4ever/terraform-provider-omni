// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package omni_test

import (
	"strings"
	"testing"

	"github.com/siderolabs/omni/client/api/omni/management"
	"github.com/siderolabs/omni/client/api/omni/specs"
	"github.com/siderolabs/omni/client/pkg/meta"

	"github.com/siderolabs/terraform-provider-omni/pkg/omni"
)

// metalMediaPreset is the minimal bare-metal preset the format cases vary from.
func metalMediaPreset() *specs.InstallationMediaConfigSpec {
	return &specs.InstallationMediaConfigSpec{
		Architecture: specs.PlatformConfigSpec_AMD64,
		Bootloader:   management.SchematicBootloader_BOOT_AUTO,
	}
}

func TestMediaTargetFromPreset(t *testing.T) {
	for _, tc := range []struct {
		spec        *specs.InstallationMediaConfigSpec
		name        string
		format      string
		expectedErr string
		expected    omni.InstallationMediaTarget
	}{
		{
			name:   "metal defaults to an ISO",
			spec:   metalMediaPreset(),
			format: "",
			expected: omni.InstallationMediaTarget{
				Kind:         management.InstallationMediaURLRequest_INSTALLATION_MEDIA_KIND_ISO,
				Platform:     omni.PlatformMetal,
				Architecture: omni.InstallationMediaArchAMD64,
			},
		},
		{
			name:   "metal iso",
			spec:   metalMediaPreset(),
			format: omni.InstallationMediaFormatISO,
			expected: omni.InstallationMediaTarget{
				Kind:         management.InstallationMediaURLRequest_INSTALLATION_MEDIA_KIND_ISO,
				Platform:     omni.PlatformMetal,
				Architecture: omni.InstallationMediaArchAMD64,
			},
		},
		{
			name:   "metal raw is an xz-compressed disk image",
			spec:   metalMediaPreset(),
			format: omni.InstallationMediaFormatRaw,
			expected: omni.InstallationMediaTarget{
				Kind:         management.InstallationMediaURLRequest_INSTALLATION_MEDIA_KIND_DISK,
				Platform:     omni.PlatformMetal,
				Architecture: omni.InstallationMediaArchAMD64,
				Format:       omni.DiskFormatRawXZ,
			},
		},
		{
			name:   "metal qcow2",
			spec:   metalMediaPreset(),
			format: omni.InstallationMediaFormatQcow2,
			expected: omni.InstallationMediaTarget{
				Kind:         management.InstallationMediaURLRequest_INSTALLATION_MEDIA_KIND_DISK,
				Platform:     omni.PlatformMetal,
				Architecture: omni.InstallationMediaArchAMD64,
				Format:       omni.InstallationMediaFormatQcow2,
			},
		},
		{
			// PXE carries no disk format: the kind implies the script the factory serves.
			name:   "metal pxe",
			spec:   metalMediaPreset(),
			format: omni.InstallationMediaFormatPXE,
			expected: omni.InstallationMediaTarget{
				Kind:         management.InstallationMediaURLRequest_INSTALLATION_MEDIA_KIND_PXE,
				Platform:     omni.PlatformMetal,
				Architecture: omni.InstallationMediaArchAMD64,
			},
		},
		{
			name: "secure boot carries through",
			spec: func() *specs.InstallationMediaConfigSpec {
				spec := metalMediaPreset()
				spec.SecureBoot = true

				return spec
			}(),
			format: omni.InstallationMediaFormatISO,
			expected: omni.InstallationMediaTarget{
				Kind:         management.InstallationMediaURLRequest_INSTALLATION_MEDIA_KIND_ISO,
				Platform:     omni.PlatformMetal,
				Architecture: omni.InstallationMediaArchAMD64,
				SecureBoot:   true,
			},
		},
		{
			name: "arm64 carries through",
			spec: func() *specs.InstallationMediaConfigSpec {
				spec := metalMediaPreset()
				spec.Architecture = specs.PlatformConfigSpec_ARM64

				return spec
			}(),
			expected: omni.InstallationMediaTarget{
				Kind:         management.InstallationMediaURLRequest_INSTALLATION_MEDIA_KIND_ISO,
				Platform:     omni.PlatformMetal,
				Architecture: omni.InstallationMediaArchARM64,
			},
		},
		{
			name: "a cloud preset rejects a format",
			spec: func() *specs.InstallationMediaConfigSpec {
				spec := metalMediaPreset()
				spec.Cloud = &specs.InstallationMediaConfigSpec_Cloud{Platform: "aws"}

				return spec
			}(),
			format:      omni.InstallationMediaFormatISO,
			expectedErr: "format is not applicable to the cloud preset",
		},
		{
			name: "an SBC preset rejects a format",
			spec: func() *specs.InstallationMediaConfigSpec {
				spec := metalMediaPreset()
				spec.Sbc = &specs.InstallationMediaConfigSpec_SBC{Overlay: "rpi_generic"}

				return spec
			}(),
			format:      omni.InstallationMediaFormatRaw,
			expectedErr: "format is not applicable to the SBC preset",
		},
		{
			// The factory serves no secure boot variant of SBC media, so this would 404 rather than
			// produce an unsigned image.
			name: "an SBC preset rejects secure boot",
			spec: func() *specs.InstallationMediaConfigSpec {
				spec := metalMediaPreset()
				spec.Sbc = &specs.InstallationMediaConfigSpec_SBC{Overlay: "rpi_generic"}
				spec.SecureBoot = true

				return spec
			}(),
			expectedErr: "which SBC media does not support",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A nil state is safe here: only the cloud and SBC branches read one, and those cases fail
			// before reaching it.
			got, err := omni.MediaTargetFromPreset(t.Context(), nil, tc.spec, tc.format)

			if tc.expectedErr != "" {
				if err == nil {
					t.Fatalf("omni.MediaTargetFromPreset() = %+v, want error containing %q", got, tc.expectedErr)
				}

				if !strings.Contains(err.Error(), tc.expectedErr) {
					t.Fatalf("omni.MediaTargetFromPreset() error = %q, want it to contain %q", err, tc.expectedErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("omni.MediaTargetFromPreset() returned unexpected error: %v", err)
			}

			if got != tc.expected {
				t.Fatalf("omni.MediaTargetFromPreset() = %+v, want %+v", got, tc.expected)
			}
		})
	}
}

func TestSchematicRequestFromPreset(t *testing.T) {
	labelsMeta := func(t *testing.T, labels map[string]string) string {
		t.Helper()

		encoded, err := meta.ImageLabels{Labels: labels}.Encode()
		if err != nil {
			t.Fatalf("failed to encode labels: %v", err)
		}

		return string(encoded)
	}

	for _, tc := range []struct {
		spec     *specs.InstallationMediaConfigSpec
		expected func(*testing.T) *management.CreateSchematicRequest
		name     string
	}{
		{
			name: "a bare preset",
			spec: metalMediaPreset(),
			expected: func(*testing.T) *management.CreateSchematicRequest {
				return &management.CreateSchematicRequest{
					SiderolinkGrpcTunnelMode: management.CreateSchematicRequest_AUTO,
					MetaValues:               map[uint32]string{},
				}
			},
		},
		{
			// The Talos version goes through untouched, empty included: Omni substitutes its own
			// default, so nothing is guessed here.
			name: "an empty Talos version is passed through",
			spec: metalMediaPreset(),
			expected: func(*testing.T) *management.CreateSchematicRequest {
				return &management.CreateSchematicRequest{
					TalosVersion:             "",
					SiderolinkGrpcTunnelMode: management.CreateSchematicRequest_AUTO,
					MetaValues:               map[uint32]string{},
				}
			},
		},
		{
			name: "a fully populated preset",
			spec: func() *specs.InstallationMediaConfigSpec {
				spec := metalMediaPreset()
				spec.TalosVersion = "1.13.5"
				spec.InstallExtensions = []string{"siderolabs/qemu-guest-agent"}
				spec.KernelArgs = "console=ttyS0 talos.platform=metal"
				spec.JoinToken = "token-id"
				spec.SecureBoot = true
				spec.GrpcTunnel = specs.GrpcTunnelMode_ENABLED
				spec.Bootloader = management.SchematicBootloader_BOOT_SD
				spec.EmbeddedMachineConfig = "version: v1alpha1\n"
				spec.MachineLabels = map[string]string{"env": "production"}

				return spec
			}(),
			expected: func(t *testing.T) *management.CreateSchematicRequest {
				return &management.CreateSchematicRequest{
					TalosVersion:             "1.13.5",
					Extensions:               []string{"siderolabs/qemu-guest-agent"},
					ExtraKernelArgs:          []string{"console=ttyS0", "talos.platform=metal"},
					JoinToken:                "token-id",
					SiderolinkGrpcTunnelMode: management.CreateSchematicRequest_ENABLED,
					Bootloader:               management.SchematicBootloader_BOOT_SD,
					EmbeddedMachineConfig:    "version: v1alpha1\n",
					MetaValues: map[uint32]string{
						meta.LabelsMeta: labelsMeta(t, map[string]string{"env": "production"}),
					},
				}
			},
		},
		{
			name: "a disabled tunnel is distinct from an unset one",
			spec: func() *specs.InstallationMediaConfigSpec {
				spec := metalMediaPreset()
				spec.GrpcTunnel = specs.GrpcTunnelMode_DISABLED

				return spec
			}(),
			expected: func(*testing.T) *management.CreateSchematicRequest {
				return &management.CreateSchematicRequest{
					SiderolinkGrpcTunnelMode: management.CreateSchematicRequest_DISABLED,
					MetaValues:               map[uint32]string{},
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A nil state is safe: only the SBC branch reads one, and none of these presets is an SBC.
			got, err := omni.SchematicRequestFromPreset(t.Context(), nil, tc.spec)
			if err != nil {
				t.Fatalf("omni.SchematicRequestFromPreset() returned unexpected error: %v", err)
			}

			if expected := tc.expected(t); !got.EqualVT(expected) {
				t.Fatalf("request = %v, want %v", got, expected)
			}
		})
	}
}

func TestFilenameFromMediaURL(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			name: "an ISO",
			in:   "https://factory.talos.dev/image/abc123/v1.13.5/metal-amd64.iso",
			want: "metal-amd64.iso",
		},
		{
			// The download token lives in the query string, so it never reaches the filename.
			name: "a token does not leak into the filename",
			in:   "https://factory.example.com/image/abc123/v1.13.5/metal-amd64.iso?token=secret",
			want: "metal-amd64.iso",
		},
		{
			name: "a PXE script has no extension",
			in:   "https://pxe.factory.talos.dev/pxe/abc123/v1.13.5/metal-amd64",
			want: "metal-amd64",
		},
		{
			name: "a compressed disk image keeps both suffixes",
			in:   "https://factory.talos.dev/image/abc123/v1.13.5/metal-arm64.raw.xz",
			want: "metal-arm64.raw.xz",
		},
		{
			name: "an unparseable URL yields nothing",
			in:   "://nonsense",
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := omni.FilenameFromMediaURL(tc.in); got != tc.want {
				t.Fatalf("omni.FilenameFromMediaURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
