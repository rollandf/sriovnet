/*
Copyright 2026 NVIDIA CORPORATION & AFFILIATES

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package docacaps

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// sampleListRepDevsOutput is a representative output of `doca_caps --list-rep-devs`
// covering representor entries of type PF/SF/VF as well as PCI-only entries without representors.
const sampleListRepDevsOutput = `
PCI: 0000:01:00.0
        representor-PCI: 0000:9f:00.0
            pci_func_type                                 PF
            hotplug                                       no
            vuid                                          e4092a71f9c1f0118000b45cb5355194MLNXS0D0F0
            rep_type                                      NET
            iface_name                                    enp1s0f0nc1pf0
            iface_index                                   9
            ib_port                                       1
            host_index                                    1
            pf_index                                      0
        representor-PCI: 0000:01:00.0
            pci_func_type                                 SF
            hotplug                                       no
            vuid                                          e4092a71f9c1f0118000b45cb5355194ECMLNXS0D0F0SF32789
            rep_type                                      NET
            iface_name                                    en1f0pf0sf123
            iface_index                                   33
            ib_port                                       19
            host_index                                    0
            pf_index                                      0
            sf_index                                      123
PCI: 0001:01:00.0
        representor-PCI: 0000:8a:00.0
            pci_func_type                                 PF
            hotplug                                       no
            vuid                                          cc027b0bf6c1f0118000b45cb5355394MLNXS0D0F0
            rep_type                                      NET
            iface_name                                    enP1s1f0nc1pf0
            iface_index                                   11
            ib_port                                       1
            host_index                                    1
            pf_index                                      0
        representor-PCI: 0000:8a:00.2
            pci_func_type                                 VF
            hotplug                                       no
            vuid                                          cc027b0bf6c1f0118000b45cb5355394MLNXS0D0F0VF1
            rep_type                                      NET
            iface_name                                    eth0
            iface_index                                   35
            ib_port                                       2
            host_index                                    1
            pf_index                                      0
            vf_index                                      0
        representor-PCI: 0000:8a:00.3
            pci_func_type                                 VF
            hotplug                                       no
            vuid                                          cc027b0bf6c1f0118000b45cb5355394MLNXS0D0F0VF2
            rep_type                                      NET
            iface_name                                    eth1
            iface_index                                   36
            ib_port                                       3
            host_index                                    1
            pf_index                                      0
            vf_index                                      1
        representor-PCI: 0001:01:00.0
            pci_func_type                                 SF
            hotplug                                       no
            vuid                                          cc027b0bf6c1f0118000b45cb5355394ECMLNXS0D0F0SF32788
            rep_type                                      NET
            iface_name                                    en1f0pf0sf100
            iface_index                                   25
            ib_port                                       18
            host_index                                    0
            pf_index                                      0
            sf_index                                      100
PCI: 0006:01:00.0
        representor-PCI: 0000:26:00.0
            pci_func_type                                 PF
            hotplug                                       no
            vuid                                          27f7781043874693bf26a22165715a32MLNXS0D0F0
            rep_type                                      NET
            iface_name                                    pf0hpf
            iface_index                                   13
            ib_port                                       1
            host_index                                    1
            pf_index                                      0
PCI: 0006:01:00.1
        representor-PCI: 0000:26:00.1
            pci_func_type                                 PF
            hotplug                                       no
            vuid                                          748aed3c30284190b8642a191d1abe1bMLNXS0D0F1
            rep_type                                      NET
            iface_name                                    pf1hpf
            iface_index                                   14
            ib_port                                       1
            host_index                                    1
            pf_index                                      1
PCI: 0001:01:00.0
PCI: 0001:01:00.1
PCI: 0000:01:00.0
PCI: 0000:01:00.1
PCI: 0000:01:00.0
`

func TestParseDocaCapsRepDevs(t *testing.T) {
	type testCase struct {
		name        string
		output      string
		runErr      error
		expectErr   bool
		errContains string
		assertDevs  func(t *testing.T, devs []*DocaCapRepDev)
	}

	tests := []testCase{
		{
			name:   "full sample output is parsed correctly",
			output: sampleListRepDevsOutput,
			assertDevs: func(t *testing.T, devs []*DocaCapRepDev) {
				assert.Len(t, devs, 8)

				validateDocaCapRepDev(t, devs[0], "0000:01:00.0", "0000:9f:00.0", "PF", "e4092a71f9c1f0118000b45cb5355194MLNXS0D0F0", "1", "0")
				validateDocaCapRepDev(t, devs[1], "0000:01:00.0", "0000:01:00.0", "SF", "e4092a71f9c1f0118000b45cb5355194ECMLNXS0D0F0SF32789", "0", "0")
				validateDocaCapRepDev(t, devs[2], "0001:01:00.0", "0000:8a:00.0", "PF", "cc027b0bf6c1f0118000b45cb5355394MLNXS0D0F0", "1", "0")
				validateDocaCapRepDev(t, devs[3], "0001:01:00.0", "0000:8a:00.2", "VF", "cc027b0bf6c1f0118000b45cb5355394MLNXS0D0F0VF1", "1", "0")
				validateDocaCapRepDev(t, devs[4], "0001:01:00.0", "0000:8a:00.3", "VF", "cc027b0bf6c1f0118000b45cb5355394MLNXS0D0F0VF2", "1", "0")
				validateDocaCapRepDev(t, devs[5], "0001:01:00.0", "0001:01:00.0", "SF", "cc027b0bf6c1f0118000b45cb5355394ECMLNXS0D0F0SF32788", "0", "0")
				validateDocaCapRepDev(t, devs[6], "0006:01:00.0", "0000:26:00.0", "PF", "27f7781043874693bf26a22165715a32MLNXS0D0F0", "1", "0")
				validateDocaCapRepDev(t, devs[7], "0006:01:00.1", "0000:26:00.1", "PF", "748aed3c30284190b8642a191d1abe1bMLNXS0D0F1", "1", "1")
			},
		},
		{
			name:   "empty output returns nil slice",
			output: "",
			assertDevs: func(t *testing.T, devs []*DocaCapRepDev) {
				assert.Nil(t, devs)
			},
		},
		{
			name:   "only blank lines returns nil slice",
			output: "\n\n   \n\t\n",
			assertDevs: func(t *testing.T, devs []*DocaCapRepDev) {
				assert.Nil(t, devs)
			},
		},
		{
			name: "PCI entries without representors yield no devs",
			output: `PCI: 0001:01:00.0
PCI: 0001:01:00.1
PCI: 0000:01:00.0
`,
			assertDevs: func(t *testing.T, devs []*DocaCapRepDev) {
				assert.Empty(t, devs)
			},
		},
		{
			name: "single PCI with single representor",
			output: `
PCI: 0006:01:00.0
        representor-PCI: 0000:26:00.0
            pci_func_type                                 PF
            vuid                                          27f7781043874693bf26a22165715a32MLNXS0D0F0
            host_index                                    1
            pf_index                                      0
`,
			assertDevs: func(t *testing.T, devs []*DocaCapRepDev) {
				assert.Len(t, devs, 1)
				validateDocaCapRepDev(t, devs[0], "0006:01:00.0", "0000:26:00.0", "PF", "27f7781043874693bf26a22165715a32MLNXS0D0F0", "1", "0")
			},
		},
		{
			name: "single-token lines under representor are ignored",
			output: `PCI: 0000:01:00.0
        representor-PCI: 0000:9f:00.0
            loneword
            vuid                                          MLNXS0D0F0
`,
			assertDevs: func(t *testing.T, devs []*DocaCapRepDev) {
				assert.Len(t, devs, 1)
				_, ok := devs[0].Attributes["loneword"]
				assert.False(t, ok)
				assert.Equal(t, "MLNXS0D0F0", devs[0].Attributes["vuid"])
				assert.Len(t, devs[0].Attributes, 1)
			},
		},
		{
			name: "attribute lines between PCI and representor are ignored",
			output: `PCI: 0000:01:00.0
            stray_key                                     stray_val
        representor-PCI: 0000:9f:00.0
            vuid                                          MLNXS0D0F0
`,
			assertDevs: func(t *testing.T, devs []*DocaCapRepDev) {
				assert.Len(t, devs, 1)
				assert.Equal(t, "MLNXS0D0F0", devs[0].Attributes["vuid"])
				_, ok := devs[0].Attributes["stray_key"]
				assert.False(t, ok)
			},
		},
		{
			name: "blank lines interspersed in valid output are tolerated",
			output: `
PCI: 0000:01:00.0

        representor-PCI: 0000:9f:00.0

            vuid                                          MLNXS0D0F0

`,
			assertDevs: func(t *testing.T, devs []*DocaCapRepDev) {
				assert.Len(t, devs, 1)
				assert.Equal(t, "MLNXS0D0F0", devs[0].Attributes["vuid"])
			},
		},
		{
			name:        "runDocaCaps error is propagated",
			runErr:      errors.New("doca_caps failed"),
			expectErr:   true,
			errContains: "doca_caps failed",
		},
		{
			name: "representor before any PCI is silently skipped",
			output: `
        representor-PCI: 0000:9f:00.0
            vuid                                          MLNXS0D0F0
PCI: 0000:01:00.0
        representor-PCI: 0000:9f:00.1
            vuid                                          REALVUID
`,
			assertDevs: func(t *testing.T, devs []*DocaCapRepDev) {
				assert.Len(t, devs, 1)
				assert.Equal(t, "0000:01:00.0", devs[0].ECPFPCIAddress)
				assert.Equal(t, "0000:9f:00.1", devs[0].RepresentorPCIAddress)
				assert.Equal(t, "REALVUID", devs[0].Attributes["vuid"])
			},
		},
		{
			name: "attribute lines before any PCI are silently skipped",
			output: `
            vuid                                          MLNXS0D0F0
PCI: 0000:01:00.0
        representor-PCI: 0000:9f:00.0
            vuid                                          REALVUID
`,
			assertDevs: func(t *testing.T, devs []*DocaCapRepDev) {
				assert.Len(t, devs, 1)
				assert.Equal(t, "REALVUID", devs[0].Attributes["vuid"])
			},
		},
		{
			name: "multi-token attribute values are dropped",
			output: `PCI: 0000:01:00.0
        representor-PCI: 0000:9f:00.0
            multi_token   value with multiple tokens
            vuid                                          MLNXS0D0F0
`,
			assertDevs: func(t *testing.T, devs []*DocaCapRepDev) {
				assert.Len(t, devs, 1)
				_, ok := devs[0].Attributes["multi_token"]
				assert.False(t, ok)
				assert.Equal(t, "MLNXS0D0F0", devs[0].Attributes["vuid"])
				assert.Len(t, devs[0].Attributes, 1)
			},
		},
		{
			name: "non-PCI top-level line resets ECPF state",
			output: `
PCI: 0000:01:00.0
SOMETHING: 0000:99:00.0
        representor-PCI: 0000:9f:00.0
            vuid                                          ORPHANED
PCI: 0000:02:00.0
        representor-PCI: 0000:aa:00.0
            vuid                                          KEPT
`,
			assertDevs: func(t *testing.T, devs []*DocaCapRepDev) {
				assert.Len(t, devs, 1)
				assert.Equal(t, "0000:02:00.0", devs[0].ECPFPCIAddress)
				assert.Equal(t, "0000:aa:00.0", devs[0].RepresentorPCIAddress)
				assert.Equal(t, "KEPT", devs[0].Attributes["vuid"])
			},
		},
		{
			name: "lines at unexpected indent levels are ignored",
			// "    representor-PCI:" sits at indent 1 (4 spaces) and is not
			// recognized; "                vuid" sits at indent 4 (16 spaces)
			// and is likewise ignored. Only the correctly-indented entries
			// produce a representor with attributes.
			output: "PCI: 0000:01:00.0\n" +
				"    representor-PCI: 0000:bb:00.0\n" +
				"        representor-PCI: 0000:9f:00.0\n" +
				"            vuid                                          KEEP\n" +
				"                ignored_key                               ignored_val\n",
			assertDevs: func(t *testing.T, devs []*DocaCapRepDev) {
				assert.Len(t, devs, 1)
				assert.Equal(t, "0000:9f:00.0", devs[0].RepresentorPCIAddress)
				assert.Equal(t, "KEEP", devs[0].Attributes["vuid"])
				_, ok := devs[0].Attributes["ignored_key"]
				assert.False(t, ok)
				assert.Len(t, devs[0].Attributes, 1)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var capturedArgs []string
			runDocaCapsCmdFn := func(args ...string) (string, error) {
				capturedArgs = args
				if tc.runErr != nil {
					return "", tc.runErr
				}
				return tc.output, nil
			}
			dc := newDOCACapsInternal(runDocaCapsCmdFn)

			devs, err := dc.parseDocaCapsRepDevs()
			if tc.expectErr {
				assert.Error(t, err)
				if tc.errContains != "" {
					assert.Contains(t, err.Error(), tc.errContains)
				}
				assert.Nil(t, devs)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, []string{listRepDevsFlags}, capturedArgs)
			if tc.assertDevs != nil {
				tc.assertDevs(t, devs)
			}
		})
	}
}

func TestGetDocaCapRepDevByVUID(t *testing.T) {
	type testCase struct {
		name           string
		output         string
		runErr         error
		vuid           string
		expectErr      bool
		errContains    string
		expectedECPF   string
		expectedRepPCI string
	}

	tests := []testCase{
		{
			name:           "unique vuid is found",
			output:         sampleListRepDevsOutput,
			vuid:           "e4092a71f9c1f0118000b45cb5355194MLNXS0D0F0",
			expectedECPF:   "0000:01:00.0",
			expectedRepPCI: "0000:9f:00.0",
		},
		{
			name:        "non-existent vuid returns not found error",
			output:      sampleListRepDevsOutput,
			vuid:        "does-not-exist",
			expectErr:   true,
			errContains: `representor device with VUID "does-not-exist" not found`,
		},
		{
			name: "vuid matching multiple devices returns error",
			output: `
PCI: 0000:01:00.0
        representor-PCI: 0000:9f:00.0
            vuid same-vuid
PCI: 0000:01:00.1
        representor-PCI: 0000:9f:00.1
            vuid same-vuid
`,
			vuid:        "same-vuid",
			expectErr:   true,
			errContains: `multiple representor devices`,
		},
		{
			name:        "parse error is wrapped",
			runErr:      errors.New("exec failed"),
			vuid:        "anything",
			expectErr:   true,
			errContains: "failed to parse doca_caps rep devs",
		},
		{
			name:        "empty output returns not found",
			output:      "",
			vuid:        "some-vuid",
			expectErr:   true,
			errContains: `representor device with VUID "some-vuid" not found`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runDocaCapsCmdFn := func(_ ...string) (string, error) {
				if tc.runErr != nil {
					return "", tc.runErr
				}
				return tc.output, nil
			}
			dc := newDOCACapsInternal(runDocaCapsCmdFn)

			dev, err := dc.GetDocaCapRepDevByVUID(tc.vuid)
			if tc.expectErr {
				assert.Error(t, err)
				if tc.errContains != "" {
					assert.Contains(t, err.Error(), tc.errContains)
				}
				assert.Nil(t, dev)
				return
			}

			assert.NoError(t, err)
			if assert.NotNil(t, dev) {
				assert.Equal(t, tc.expectedECPF, dev.ECPFPCIAddress)
				assert.Equal(t, tc.expectedRepPCI, dev.RepresentorPCIAddress)
				assert.Equal(t, tc.vuid, dev.Attributes["vuid"])
			}
		})
	}
}

func validateDocaCapRepDev(
	t *testing.T,
	dev *DocaCapRepDev,
	expectedECPF string,
	expectedRepPCI string,
	expectedType string,
	expectedVUID string,
	expectedHostIndex string,
	expectedPfIndex string,
) {
	assert.Equal(t, expectedECPF, dev.ECPFPCIAddress, "ECPFPCIAddress mismatch")
	assert.Equal(t, expectedRepPCI, dev.RepresentorPCIAddress, "RepresentorPCIAddress mismatch")
	assert.Equal(t, expectedType, dev.Attributes["pci_func_type"], "pci_func_type mismatch")
	assert.Equal(t, expectedVUID, dev.Attributes["vuid"], "vuid mismatch")
	assert.Equal(t, expectedHostIndex, dev.Attributes["host_index"], "host_index mismatch")
	assert.Equal(t, expectedPfIndex, dev.Attributes["pf_index"], "pf_index mismatch")
}
