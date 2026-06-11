/*
Copyright 2023 NVIDIA CORPORATION & AFFILIATES

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

package sriovnet

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vishvananda/netlink"

	utilfs "github.com/k8snetworkplumbingwg/sriovnet/pkg/utils/filesystem"
	"github.com/k8snetworkplumbingwg/sriovnet/pkg/utils/netlinkops"
	netlinkopsMocks "github.com/k8snetworkplumbingwg/sriovnet/pkg/utils/netlinkops/mocks"
)

func ptrTo[T any](v T) *T {
	return &v
}

type repContext struct {
	// Name is the representor netdev name
	Name string // create files /sys/bus/pci/devices/<vf addr>/physfn/net/<Name> , /sys/class/net/<Name>
	// PhysPortName is the phys_port_name of the representor netdev
	PhysPortName string // conditionally create if non empty under /sys/class/net/<Name>/phys_port_name
	// PhysSwitchID is the phys_switch_id of the representor netdev
	PhysSwitchID string // conditionally create if non empty under /sys/class/net/<Name>/phys_switch_id
}

// setUpRepPhysFiles sets up phys_port_name and phys_switch_id files for specified representor
// Note: should not be called directly as it expects FakeFs and representor
// path to be initialized beforehand.
func setUpRepPhysFiles(rep *repContext) error {
	var err error

	if rep.PhysPortName != "" {
		physPortNamePath := filepath.Join(NetSysDir, rep.Name, netdevPhysPortName)
		physPortNameFile, _ := utilfs.Fs.Create(physPortNamePath)
		_, err = physPortNameFile.Write([]byte(rep.PhysPortName))
		if err != nil {
			return err
		}
	}

	if rep.PhysSwitchID != "" {
		physSwitchIDPath := filepath.Join(NetSysDir, rep.Name, netdevPhysSwitchID)
		physSwitchIDFile, _ := utilfs.Fs.Create(physSwitchIDPath)
		_, err = physSwitchIDFile.Write([]byte(rep.PhysSwitchID))
		if err != nil {
			return err
		}
	}

	return nil
}

// setUpRepresentorLayout sets up the representor filesystem layout.
// Note: should not be called directly as it expects FakeFs to be initialized beforehand.
func setUpRepresentorLayout(rep *repContext) error {
	// This method assumes FakeFs it already set up
	_, ok := utilfs.Fs.(*utilfs.FakeFs)
	if !ok {
		return fmt.Errorf("fakeFs was not initialized")
	}

	path := filepath.Join(NetSysDir, rep.Name)
	err := utilfs.Fs.MkdirAll(path, os.FileMode(0755))
	if err != nil {
		return err
	}

	return setUpRepPhysFiles(rep)
}

// setupUplinkRepresentorEnv sets up the uplink representor and related VF representors filesystem layout.
//
//nolint:unparam
func setupUplinkRepresentorEnv(t *testing.T, uplink *repContext, pfPciAddress string, vfPciAddress string, reps []*repContext) func() {
	var err error
	teardown := setupFakeFs(t)

	defer func() {
		if err != nil {
			teardown()
			t.Errorf("setupUplinkRepresentorEnv: %v", err)
		}
	}()

	if err = utilfs.Fs.MkdirAll(NetSysDir, os.FileMode(0755)); err != nil {
		return teardown
	}
	if err = utilfs.Fs.MkdirAll(PciSysDir, os.FileMode(0755)); err != nil {
		return teardown
	}

	// For a VF, create a symlink file named physfn to link with the parent PF device.
	if vfPciAddress != "" {
		if err = utilfs.Fs.MkdirAll(filepath.Join(PciSysDir, pfPciAddress), os.FileMode(0755)); err != nil {
			return teardown
		}
		if err = utilfs.Fs.MkdirAll(filepath.Join(PciSysDir, vfPciAddress), os.FileMode(0755)); err != nil {
			return teardown
		}
		if err = utilfs.Fs.Symlink(
			filepath.Join(PciSysDir, pfPciAddress),
			filepath.Join(PciSysDir, vfPciAddress, "physfn")); err != nil {
			return teardown
		}
	}

	// setupRep creates the entry under the PF's net directory (for sysfs ReadDir) and
	// the /sys/class/net entry (for isSwitchdev / getNetDevPhysPortName).
	setupRep := func(rep *repContext) error {
		if err := utilfs.Fs.MkdirAll(filepath.Join(PciSysDir, pfPciAddress, "net", rep.Name), os.FileMode(0755)); err != nil {
			return err
		}
		if err := utilfs.Fs.MkdirAll(filepath.Join(NetSysDir, rep.Name), os.FileMode(0755)); err != nil {
			return err
		}
		return setUpRepPhysFiles(rep)
	}

	if uplink != nil {
		if err = setupRep(uplink); err != nil {
			return teardown
		}
	}
	for _, rep := range reps {
		if err = setupRep(rep); err != nil {
			return teardown
		}
	}

	return teardown
}

// setupRepresentorEnv sets up VF representors filesystem layout.
func setupRepresentorEnv(t *testing.T, vfReps []*repContext) func() {
	var err error
	teardown := setupFakeFs(t)

	defer func() {
		if err != nil {
			teardown()
			t.Errorf("setupRepresentorEnv, got %v", err)
		}
	}()

	for _, rep := range vfReps {
		err = setUpRepresentorLayout(rep)
		if err != nil {
			t.Errorf("setupRepresentorEnv, got %v", err)
		}
	}

	return teardown
}

// setupDPUConfigFileForPort sets the config file content for a specific DPU port of a given uplink
func setupDPUConfigFileForPort(t *testing.T, uplink, portName, fileContent string) {
	// This method assumes FakeFs it already set up
	assert.IsType(t, &utilfs.FakeFs{}, utilfs.Fs)

	path := filepath.Join(NetSysDir, uplink, "smart_nic", portName)
	err := utilfs.Fs.MkdirAll(path, os.FileMode(0755))
	assert.NoError(t, err)

	repConfigFilePath := filepath.Join(path, "config")
	repConfigFileName, _ := utilfs.Fs.Create(repConfigFilePath)
	_, err = repConfigFileName.Write([]byte(fileContent))
	assert.NoError(t, err)
}

func setupRepresentorEnvForGetVfRepresentor(t *testing.T, uplinkPciAddress string, uplink *repContext, vfReps []repContext) func() {
	var err error
	teardown := setupFakeFs(t)

	defer func() {
		if err != nil {
			teardown()
			t.Errorf("setupRepresentorEnvForGetVfRepresentor, got %v", err)
		}
	}()

	// create /sys/class/net dir
	err = utilfs.Fs.MkdirAll(NetSysDir, os.FileMode(0755))
	if err != nil {
		return teardown
	}

	// create /sys/bus/pci/devices dir
	err = utilfs.Fs.MkdirAll(PciSysDir, os.FileMode(0755))
	if err != nil {
		return teardown
	}

	// create net folder for uplink pci address
	pfNetDevicePath := filepath.Join(PciSysDir, uplinkPciAddress, "net")
	err = utilfs.Fs.MkdirAll(pfNetDevicePath, os.FileMode(0755))
	if err != nil {
		return teardown
	}

	// create uplink device and its netdev (if provided)
	if uplink != nil {
		pfNetUplinkPath := filepath.Join(PciSysDir, uplinkPciAddress, "net", uplink.Name)
		err = utilfs.Fs.MkdirAll(pfNetUplinkPath, os.FileMode(0755))
		if err != nil {
			return teardown
		}

		// with a link in /sys/class/net
		err = utilfs.Fs.Symlink(pfNetUplinkPath, filepath.Join(NetSysDir, uplink.Name))
		if err != nil {
			return teardown
		}
		// and a symlink for the uplink device under /sys/class/net
		err = utilfs.Fs.Symlink(filepath.Join(PciSysDir, uplinkPciAddress), filepath.Join(pfNetUplinkPath, "device"))
		if err != nil {
			return teardown
		}

		// create phys_port_name and phys_switch_id files for the uplink
		if err = setUpRepPhysFiles(uplink); err != nil {
			return teardown
		}
	}

	for _, rep := range vfReps {
		// Create representor directory under the uplink's device/net path
		pfNetPath := filepath.Join(PciSysDir, uplinkPciAddress, "net")
		repPath := filepath.Join(pfNetPath, rep.Name)
		repLink := filepath.Join(NetSysDir, rep.Name)

		err = utilfs.Fs.MkdirAll(repPath, os.FileMode(0755))
		if err != nil {
			return teardown
		}

		// Create symlink from /sys/class/net/<rep_name> to the rep path
		_ = utilfs.Fs.Symlink(repPath, repLink)

		if err = setUpRepPhysFiles(&rep); err != nil {
			return teardown
		}
	}

	return teardown
}

func TestGetVfRepresentor(t *testing.T) {
	uplinkPciAddress := "0000:03:00.0"

	t.Run("Get VF representor from sysfs", func(t *testing.T) {
		tcases := []struct {
			name             string
			uplinkPciAddress string
			uplink           *repContext
			vfReps           []repContext
			vfIndex          int
			expectedVFRep    string
			shouldFail       bool
			expectedErr      error
		}{
			{
				name:             "VF representor found",
				uplinkPciAddress: uplinkPciAddress,
				uplink:           &repContext{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
				vfReps: []repContext{
					{Name: "eth0", PhysPortName: "pf0vf0", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth1", PhysPortName: "pf0vf1", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth2", PhysPortName: "pf0vf2", PhysSwitchID: "c2cfc60003a1420c"},
				},
				vfIndex:       2,
				expectedVFRep: "eth2",
				shouldFail:    false,
			},
			{
				name:             "VF representor found - uplink not present, PCI address provided",
				uplinkPciAddress: uplinkPciAddress,
				vfReps: []repContext{
					{Name: "eth0", PhysPortName: "pf0vf0", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth1", PhysPortName: "pf0vf1", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth2", PhysPortName: "pf0vf2", PhysSwitchID: "c2cfc60003a1420c"},
				},
				vfIndex:       2,
				expectedVFRep: "eth2",
				shouldFail:    false,
			},
			{
				name:             "VF representor not found - index doesn't exist",
				uplinkPciAddress: uplinkPciAddress,
				uplink:           &repContext{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
				vfReps: []repContext{
					{Name: "eth0", PhysPortName: "pf0vf0", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth1", PhysPortName: "pf0vf1", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth2", PhysPortName: "pf0vf2", PhysSwitchID: "c2cfc60003a1420c"},
				},
				vfIndex:       5,
				expectedVFRep: "",
				shouldFail:    true,
				expectedErr:   ErrRepresentorNotFound,
			},
			{
				name:             "VF representor not found - no representors",
				uplinkPciAddress: uplinkPciAddress,
				uplink:           &repContext{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
				vfReps:           nil,
				vfIndex:          0,
				expectedVFRep:    "",
				shouldFail:       true,
			},
			{
				name:             "VF representor not found - no representors and no uplink",
				uplinkPciAddress: uplinkPciAddress,
				vfReps:           nil,
				vfIndex:          0,
				expectedVFRep:    "",
				shouldFail:       true,
				expectedErr:      ErrRepresentorNotFound,
			},
			{
				name:             "VF representor not found - invalid phys_port_name",
				uplinkPciAddress: uplinkPciAddress,
				uplink:           &repContext{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
				vfReps: []repContext{
					{Name: "eth0", PhysPortName: "invalid", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth1", PhysPortName: "pf0sf1", PhysSwitchID: "c2cfc60003a1420c"}, // SF instead of VF
				},
				vfIndex:       0,
				expectedVFRep: "",
				shouldFail:    true,
				expectedErr:   ErrRepresentorNotFound,
			},
			{
				name:             "VF representor not found - missing phys_port_name",
				uplinkPciAddress: uplinkPciAddress,
				uplink:           &repContext{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
				vfReps: []repContext{
					{Name: "eth0", PhysPortName: "", PhysSwitchID: "c2cfc60003a1420c"}, // No phys_port_name
					{Name: "eth1", PhysPortName: "pf0vf1", PhysSwitchID: "c2cfc60003a1420c"},
				},
				vfIndex:       0,
				expectedVFRep: "",
				shouldFail:    true,
				expectedErr:   ErrRepresentorNotFound,
			},
			{
				name:             "uplink is not switchdev",
				uplinkPciAddress: uplinkPciAddress,
				uplink:           &repContext{Name: "eth0", PhysPortName: "", PhysSwitchID: ""},
				vfReps:           []repContext{},
				vfIndex:          0,
				expectedVFRep:    "",
				shouldFail:       true,
			},
			{
				name:             "VF representor found with mixed representors",
				uplinkPciAddress: uplinkPciAddress,
				uplink:           &repContext{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
				vfReps: []repContext{
					{Name: "eth0", PhysPortName: "invalid", PhysSwitchID: "c2cfc60003a1420c"}, // Invalid
					{Name: "eth1", PhysPortName: "pf0vf0", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth2", PhysPortName: "pf0sf1", PhysSwitchID: "c2cfc60003a1420c"}, // SF rep
					{Name: "eth3", PhysPortName: "pf0vf2", PhysSwitchID: "c2cfc60003a1420c"},
				},
				vfIndex:       2,
				expectedVFRep: "eth3",
				shouldFail:    false,
			},
			{
				name:             "VF representor found - mixed external representor",
				uplinkPciAddress: uplinkPciAddress,
				uplink:           &repContext{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
				vfReps: []repContext{
					{Name: "eth2", PhysPortName: "c1pf0vf1", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth3", PhysPortName: "c1pf0vf2", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth4", PhysPortName: "pf0vf1", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth5", PhysPortName: "pf0vf2", PhysSwitchID: "c2cfc60003a1420c"},
				},
				vfIndex:       2,
				expectedVFRep: "eth5",
				shouldFail:    false,
			},
			{
				name:             "VF representor not found - only external representor",
				uplinkPciAddress: uplinkPciAddress,
				uplink:           &repContext{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
				vfReps: []repContext{
					{Name: "eth2", PhysPortName: "c1pf0vf1", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth3", PhysPortName: "c1pf0vf2", PhysSwitchID: "c2cfc60003a1420c"},
				},
				vfIndex:       2,
				expectedVFRep: "",
				shouldFail:    true,
			},
		}

		for _, tcase := range tcases {
			t.Run(tcase.name, func(t *testing.T) {
				// mock netlink calls, trigger failure to fallback to sysfs
				nlOpsMock := netlinkopsMocks.NewMockNetlinkOps(t)
				netlinkops.SetNetlinkOps(nlOpsMock)
				defer netlinkops.ResetNetlinkOps()
				nlOpsMock.On("DevLinkGetDevicePortList", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(
					nil, fmt.Errorf("failed to get devlink ports")).Maybe()

				teardown := setupRepresentorEnvForGetVfRepresentor(t, tcase.uplinkPciAddress, tcase.uplink, tcase.vfReps)
				defer teardown()

				var vfRep string
				var err error

				if tcase.uplink == nil {
					// use uplink PCI address if no uplink netdev
					vfRep, err = GetVfRepresentor(tcase.uplinkPciAddress, tcase.vfIndex)
				} else {
					// use uplink netdev name if provided
					vfRep, err = GetVfRepresentor(tcase.uplink.Name, tcase.vfIndex)
				}

				if tcase.shouldFail {
					assert.Error(t, err)
					if tcase.expectedErr != nil {
						assert.ErrorIs(t, err, tcase.expectedErr)
					}
				} else {
					assert.NoError(t, err)
					assert.Equal(t, tcase.expectedVFRep, vfRep)
				}
			})
		}
	})

	t.Run("Get VF representor from devlink", func(t *testing.T) {
		// Test with devlink ports available
		uplinkPhysDevlinkPort := &netlink.DevlinkPort{
			NetdeviceName:    "p0",
			PortFlavour:      uint16(PORT_FLAVOUR_PHYSICAL),
			ControllerNumber: ptrTo(uint32(0)),
			PortNumber:       ptrTo(uint32(0)),
		}
		vfDevlinkPorts := []*netlink.DevlinkPort{
			// vf0 port
			{
				NetdeviceName:    "pf0vf0",
				PortFlavour:      uint16(PORT_FLAVOUR_PCI_VF),
				ControllerNumber: ptrTo(uint32(0)),
				VfNumber:         ptrTo(uint16(0)),
			},
			// vf1 external port
			{
				NetdeviceName:    "c1pf0vf1",
				PortFlavour:      uint16(PORT_FLAVOUR_PCI_VF),
				ControllerNumber: ptrTo(uint32(1)),
				VfNumber:         ptrTo(uint16(1)),
			},
			// vf1 port
			{
				NetdeviceName:    "pf0vf1",
				PortFlavour:      uint16(PORT_FLAVOUR_PCI_VF),
				ControllerNumber: ptrTo(uint32(0)),
				VfNumber:         ptrTo(uint16(1)),
			},
		}

		tcases := []struct {
			name             string
			uplinkPciAddress string
			uplink           *repContext
			devlinkPorts     []*netlink.DevlinkPort
			uplinkArg        string
		}{
			{
				name:             "uplink representor present",
				uplinkPciAddress: uplinkPciAddress,
				uplink:           &repContext{Name: "p0", PhysPortName: "p0", PhysSwitchID: "111111"},
				devlinkPorts:     append([]*netlink.DevlinkPort{uplinkPhysDevlinkPort}, vfDevlinkPorts...),
				uplinkArg:        "p0",
			},
			{
				name:             "no uplink representor",
				uplinkPciAddress: uplinkPciAddress,
				uplink:           nil,
				devlinkPorts:     vfDevlinkPorts,
				uplinkArg:        uplinkPciAddress,
			},
		}

		for _, tc := range tcases {
			t.Run(tc.name, func(t *testing.T) {
				teardown := setupRepresentorEnvForGetVfRepresentor(t, uplinkPciAddress, tc.uplink, nil)
				defer teardown()

				nlOpsMock := netlinkopsMocks.NewMockNetlinkOps(t)
				netlinkops.SetNetlinkOps(nlOpsMock)
				defer netlinkops.ResetNetlinkOps()

				nlOpsMock.On("DevLinkGetDevicePortList", "pci", uplinkPciAddress).Return(
					tc.devlinkPorts, nil)

				vfRep, err := GetVfRepresentor(tc.uplinkArg, 1)
				assert.NoError(t, err)
				assert.Equal(t, "pf0vf1", vfRep)
			})
		}

	})

	// Test edge case: uplink directory doesn't exist (filesystem error)
	t.Run("uplink directory doesn't exist", func(t *testing.T) {
		teardown := setupFakeFs(t)
		defer teardown()

		_, err := GetVfRepresentor("nonexistent_uplink", 0)
		assert.Error(t, err)
	})
}

func TestGetUplinkRepresentorWithPhysPortName(t *testing.T) {
	// pfPciAddress is the PF PCI address used for all VF test cases.
	const pfPciAddress = "0000:03:00.0"

	tcases := []struct {
		name                 string
		pfPciAddress         string
		vfPciAddress         string
		uplinkRep            *repContext
		vfReps               []*repContext
		expectedUplinkNetdev string
		shouldFail           bool
		expectedErr          error
	}{
		{
			name:         "uplink representor exists",
			pfPciAddress: pfPciAddress,
			vfPciAddress: "0000:03:00.4",
			uplinkRep:    &repContext{Name: "eth0", PhysPortName: "p0", PhysSwitchID: "111111"},
			vfReps: []*repContext{
				{Name: "enp_0", PhysPortName: "pf0vf0", PhysSwitchID: "111111"},
				{Name: "enp_1", PhysPortName: "pf0vf1", PhysSwitchID: "111111"},
			},
			expectedUplinkNetdev: "eth0",
			shouldFail:           false,
		},
		{
			name:                 "uplink representor exists with PF instead of VF",
			pfPciAddress:         pfPciAddress,
			vfPciAddress:         "",
			uplinkRep:            &repContext{Name: "eth0", PhysPortName: "p0", PhysSwitchID: "111111"},
			vfReps:               []*repContext{},
			expectedUplinkNetdev: "eth0",
			shouldFail:           false,
		},
		{
			name:         "uplink representor does not exist",
			pfPciAddress: pfPciAddress,
			vfPciAddress: "0000:03:00.4",
			uplinkRep:    &repContext{Name: "eth0", PhysPortName: "", PhysSwitchID: ""},
			vfReps: []*repContext{
				{Name: "enp_0", PhysPortName: "pf0vf0", PhysSwitchID: "111111"},
				{Name: "enp_1", PhysPortName: "pf0vf1", PhysSwitchID: "111111"},
			},
			expectedUplinkNetdev: "",
			shouldFail:           true,
			expectedErr:          ErrRepresentorNotFound,
		},
		{
			name:         "uplink representor missing switch id",
			pfPciAddress: pfPciAddress,
			vfPciAddress: "0000:03:00.4",
			uplinkRep:    &repContext{Name: "eth0", PhysPortName: "p0", PhysSwitchID: ""},
			vfReps: []*repContext{
				{Name: "enp_0", PhysPortName: "pf0vf0", PhysSwitchID: "111111"},
				{Name: "enp_1", PhysPortName: "pf0vf1", PhysSwitchID: "111111"},
			},
			expectedUplinkNetdev: "",
			shouldFail:           true,
			expectedErr:          ErrRepresentorNotFound,
		},
		{
			name:                 "no representors",
			pfPciAddress:         pfPciAddress,
			vfPciAddress:         "0000:03:00.4",
			uplinkRep:            &repContext{Name: "eth0", PhysPortName: "", PhysSwitchID: ""},
			vfReps:               []*repContext{},
			expectedUplinkNetdev: "",
			shouldFail:           true,
			expectedErr:          ErrRepresentorNotFound,
		},
		{
			name:                 "missing uplink",
			pfPciAddress:         pfPciAddress,
			vfPciAddress:         "0000:03:00.4",
			uplinkRep:            nil,
			vfReps:               []*repContext{},
			expectedUplinkNetdev: "",
			shouldFail:           true,
		},
	}

	for _, tcase := range tcases {
		t.Run(tcase.name, func(t *testing.T) {
			// mock netlink calls, trigger failure to fallback to sysfs
			nlOpsMock := netlinkopsMocks.NewMockNetlinkOps(t)
			netlinkops.SetNetlinkOps(nlOpsMock)
			defer netlinkops.ResetNetlinkOps()
			nlOpsMock.On("DevLinkGetAllPortList").Return(
				nil, fmt.Errorf("devlink not available")).Maybe()

			teardown := setupUplinkRepresentorEnv(t, tcase.uplinkRep, tcase.pfPciAddress, tcase.vfPciAddress, tcase.vfReps)
			defer teardown()

			// query with the VF address when provided, otherwise the PF address.
			queryAddr := tcase.vfPciAddress
			if queryAddr == "" {
				queryAddr = tcase.pfPciAddress
			}

			uplinkNetdev, err := GetUplinkRepresentor(queryAddr)
			if tcase.shouldFail {
				assert.Error(t, err)
				if tcase.expectedErr != nil {
					assert.ErrorIs(t, err, tcase.expectedErr)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tcase.expectedUplinkNetdev, uplinkNetdev)
			}
		})
	}
}

func TestGetUplinkRepresentorForPfSuccess(t *testing.T) {
	pfPciAddress := "0000:03:00.0"
	uplinkRep := &repContext{Name: "eth0", PhysPortName: "p0", PhysSwitchID: "111111"}

	// mock netlink calls, trigger failure to fallback to sysfs
	nlOpsMock := netlinkopsMocks.NewMockNetlinkOps(t)
	netlinkops.SetNetlinkOps(nlOpsMock)
	defer netlinkops.ResetNetlinkOps()
	nlOpsMock.On("DevLinkGetAllPortList").Return(
		nil, fmt.Errorf("devlink not available")).Maybe()

	// vfPciAddress is empty → PF case: no physfn symlink, net dir created under pfPciAddress.
	teardown := setupUplinkRepresentorEnv(t, uplinkRep, pfPciAddress, "", nil)
	defer teardown()
	uplinkNetdev, err := GetUplinkRepresentor(pfPciAddress)
	assert.NoError(t, err)
	assert.Equal(t, "eth0", uplinkNetdev)
}

func TestGetUplinkRepresentorErrorEmptySwID(t *testing.T) {
	var testErr error
	pfPciAddress := "0000:03:00.0"
	vfPciAddress := "0000:03:00.4"
	uplinkRep := &repContext{Name: "eth0", PhysPortName: "", PhysSwitchID: ""}

	// mock netlink calls, trigger failure to fallback to sysfs
	nlOpsMock := netlinkopsMocks.NewMockNetlinkOps(t)
	netlinkops.SetNetlinkOps(nlOpsMock)
	defer netlinkops.ResetNetlinkOps()
	nlOpsMock.On("DevLinkGetAllPortList").Return(
		nil, fmt.Errorf("devlink not available")).Maybe()

	teardown := setupUplinkRepresentorEnv(t, uplinkRep, pfPciAddress, vfPciAddress, nil)
	defer teardown()
	// Overwrite the phys_switch_id file with an empty value to simulate a non-switchdev uplink.
	swIDFile := filepath.Join(NetSysDir, "eth0", netdevPhysSwitchID)
	swID, testErr := utilfs.Fs.Create(swIDFile)
	defer func() {
		if testErr != nil {
			t.Errorf("TestGetUplinkRepresentorErrorEmptySwID: %v", testErr)
		}
	}()
	_, testErr = swID.Write([]byte(""))
	uplinkNetdev, err := GetUplinkRepresentor(vfPciAddress)
	assert.Error(t, err)
	assert.Equal(t, "", uplinkNetdev)
	assert.ErrorIs(t, err, ErrRepresentorNotFound)
}

func TestGetUplinkRepresentorDevlink(t *testing.T) {
	const pfPciAddress = "0000:03:00.0"

	uplinkRep := &repContext{Name: "p0", PhysPortName: "p0", PhysSwitchID: "aabbcc"}

	devlinkPhysicalPort := &netlink.DevlinkPort{
		NetdeviceName:    "p0",
		PortFlavour:      uint16(PORT_FLAVOUR_PHYSICAL),
		ControllerNumber: ptrTo(uint32(0)),
		PortNumber:       ptrTo(uint32(0)),
	}
	devlinkNonPhysicalPort := &netlink.DevlinkPort{
		NetdeviceName:    "pf0hpf",
		PortFlavour:      uint16(PORT_FLAVOUR_PCI_PF),
		ControllerNumber: ptrTo(uint32(0)),
		PortNumber:       ptrTo(uint32(0)),
	}

	devlinkPhysicalPortNoNetdev := &netlink.DevlinkPort{
		NetdeviceName:    "",
		PortFlavour:      uint16(PORT_FLAVOUR_PHYSICAL),
		ControllerNumber: ptrTo(uint32(0)),
		PortNumber:       ptrTo(uint32(0)),
	}

	tcases := []struct {
		name                 string
		vfPciAddress         string
		uplinkRep            *repContext
		devlinkPorts         []*netlink.DevlinkPort
		devlinkPortsErr      error
		expectedUplinkNetdev string
	}{
		{
			name:                 "returns uplink from devlink for PF",
			vfPciAddress:         "",
			uplinkRep:            uplinkRep,
			devlinkPorts:         []*netlink.DevlinkPort{devlinkPhysicalPort},
			expectedUplinkNetdev: "p0",
		},
		{
			// physfn symlink is created by setupUplinkRepresentorEnv for the VF case.
			name:                 "returns uplink from devlink for VF",
			vfPciAddress:         "0000:03:00.4",
			uplinkRep:            uplinkRep,
			devlinkPorts:         []*netlink.DevlinkPort{devlinkPhysicalPort},
			expectedUplinkNetdev: "p0",
		},
		{
			name:                 "falls back to sysfs when devlink port lookup fails",
			vfPciAddress:         "",
			uplinkRep:            uplinkRep,
			devlinkPortsErr:      fmt.Errorf("devlink port not available"),
			expectedUplinkNetdev: "p0",
		},
		{
			// only a non-physical port is reported, so candidateNetdevs is empty.
			name:                 "falls back to sysfs when devlink physical port not found",
			vfPciAddress:         "",
			uplinkRep:            uplinkRep,
			devlinkPorts:         []*netlink.DevlinkPort{devlinkNonPhysicalPort},
			expectedUplinkNetdev: "p0",
		},
		{
			name:                 "falls back to sysfs when devlink port has no netdev name",
			vfPciAddress:         "",
			uplinkRep:            uplinkRep,
			devlinkPorts:         []*netlink.DevlinkPort{devlinkPhysicalPortNoNetdev},
			expectedUplinkNetdev: "p0",
		},
	}

	for _, tcase := range tcases {
		t.Run(tcase.name, func(t *testing.T) {
			teardown := setupUplinkRepresentorEnv(t, tcase.uplinkRep, pfPciAddress, tcase.vfPciAddress, nil)
			defer teardown()

			nlOpsMock := netlinkopsMocks.NewMockNetlinkOps(t)
			netlinkops.SetNetlinkOps(nlOpsMock)
			defer netlinkops.ResetNetlinkOps()

			nlOpsMock.On("DevLinkGetAllPortList").Return(tcase.devlinkPorts, tcase.devlinkPortsErr)

			// query with the VF address when provided, otherwise the PF address.
			queryAddr := tcase.vfPciAddress
			if queryAddr == "" {
				queryAddr = pfPciAddress
			}

			uplink, err := GetUplinkRepresentor(queryAddr)
			assert.NoError(t, err)
			assert.Equal(t, tcase.expectedUplinkNetdev, uplink)
		})
	}
}

func TestGetVfRepresentorDPU(t *testing.T) {
	tcases := []struct {
		name          string
		vfReps        []*repContext
		pfID          string
		vfID          string
		expectedVFRep string
		shouldFail    bool
		expectedErr   error
	}{
		{
			name: "Host VFs only",
			vfReps: []*repContext{
				{Name: "eth0", PhysPortName: "c1pf0vf0", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "eth1", PhysPortName: "c1pf0vf1", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "eth2", PhysPortName: "c1pf0vf2", PhysSwitchID: "c2cfc60003a1420c"},
			},
			pfID:          "0",
			vfID:          "2",
			expectedVFRep: "eth2",
			shouldFail:    false,
		},
		{
			name: "Host VFs and DPU VFs",
			vfReps: []*repContext{
				{Name: "eth0", PhysPortName: "c1pf0vf0", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "eth1", PhysPortName: "pf0vf0", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "eth2", PhysPortName: "pf0vf2", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "eth3", PhysPortName: "c1pf0vf2", PhysSwitchID: "c2cfc60003a1420c"},
			},
			pfID:          "0",
			vfID:          "2",
			expectedVFRep: "eth3",
			shouldFail:    false,
		},
		{
			name: "Host VFs only - Legacy (rep names dont have controller prefix)",
			vfReps: []*repContext{
				{Name: "eth0", PhysPortName: "pf0vf0", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "eth1", PhysPortName: "pf0vf1", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "eth2", PhysPortName: "pf0vf2", PhysSwitchID: "c2cfc60003a1420c"},
			},
			pfID:          "0",
			vfID:          "2",
			expectedVFRep: "eth2",
			shouldFail:    false,
		},
		{
			name: "VF representor not found",
			vfReps: []*repContext{
				{Name: "eth0", PhysPortName: "c1pf0vf0", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "eth1", PhysPortName: "c1pf0vf1", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "eth2", PhysPortName: "c1pf0vf2", PhysSwitchID: "c2cfc60003a1420c"},
			},
			pfID:          "0",
			vfID:          "5",
			expectedVFRep: "",
			shouldFail:    true,
			expectedErr:   ErrRepresentorNotFound,
		},
		{
			name:          "invalid pfID",
			vfReps:        []*repContext{},
			pfID:          "3",
			vfID:          "5",
			expectedVFRep: "",
			shouldFail:    true,
		},
		{
			name:          "invalid pfID - 2",
			vfReps:        []*repContext{},
			pfID:          "bla",
			vfID:          "5",
			expectedVFRep: "",
			shouldFail:    true,
		},
		{
			name:          "invalid vfID",
			vfReps:        []*repContext{},
			pfID:          "0",
			vfID:          "bla",
			expectedVFRep: "",
			shouldFail:    true,
		},
	}

	for _, tcase := range tcases {
		t.Run(tcase.name, func(t *testing.T) {
			teardown := setupRepresentorEnv(t, tcase.vfReps)
			defer teardown()
			vfRep, err := GetVfRepresentorDPU(tcase.pfID, tcase.vfID)
			if tcase.shouldFail {
				assert.Error(t, err)
				if tcase.expectedErr != nil {
					assert.ErrorIs(t, err, tcase.expectedErr)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tcase.expectedVFRep, vfRep)
			}
		})
	}
}

func TestGetPfRepresentorDPU(t *testing.T) {
	tcases := []struct {
		name          string
		pfReps        []*repContext
		pfID          string
		expectedPfRep string
		shouldFail    bool
		expectedErr   error
	}{
		{
			name: "PF representor with controller index",
			pfReps: []*repContext{
				{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "p1", PhysPortName: "p1", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "eth0", PhysPortName: "c1pf0", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "eth1", PhysPortName: "c1pf1", PhysSwitchID: "c2cfc60003a1420c"},
			},
			pfID:          "0",
			expectedPfRep: "eth0",
			shouldFail:    false,
		},
		{
			name: "PF representor with controller index - pf1",
			pfReps: []*repContext{
				{Name: "eth0", PhysPortName: "c1pf0", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "eth1", PhysPortName: "c1pf1", PhysSwitchID: "c2cfc60003a1420c"},
			},
			pfID:          "1",
			expectedPfRep: "eth1",
			shouldFail:    false,
		},
		{
			name: "PF representor without controller index (legacy)",
			pfReps: []*repContext{
				{Name: "eth0", PhysPortName: "pf0", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "eth1", PhysPortName: "pf1", PhysSwitchID: "c2cfc60003a1420c"},
			},
			pfID:          "1",
			expectedPfRep: "eth1",
			shouldFail:    false,
		},
		{
			name: "PF representor not found",
			pfReps: []*repContext{
				{Name: "eth0", PhysPortName: "c1pf0", PhysSwitchID: "c2cfc60003a1420c"},
			},
			pfID:          "1",
			expectedPfRep: "",
			shouldFail:    true,
			expectedErr:   ErrRepresentorNotFound,
		},
		{
			name:          "invalid pfID",
			pfReps:        []*repContext{},
			pfID:          "3",
			expectedPfRep: "",
			shouldFail:    true,
		},
		{
			name:          "invalid pfID - non-numeric",
			pfReps:        []*repContext{},
			pfID:          "bla",
			expectedPfRep: "",
			shouldFail:    true,
		},
	}

	for _, tcase := range tcases {
		t.Run(tcase.name, func(t *testing.T) {
			teardown := setupRepresentorEnv(t, tcase.pfReps)
			defer teardown()
			pfRep, err := GetPfRepresentorDPU(tcase.pfID)
			if tcase.shouldFail {
				assert.Error(t, err)
				if tcase.expectedErr != nil {
					assert.ErrorIs(t, err, tcase.expectedErr)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tcase.expectedPfRep, pfRep)
			}
		})
	}
}

func TestGetSfRepresentor(t *testing.T) {
	uplinkPciAddress := "0000:03:00.0"

	t.Run("Get SF representor from sysfs", func(t *testing.T) {
		tcases := []struct {
			name             string
			uplinkPciAddress string
			uplink           *repContext
			sfReps           []repContext
			uplinkArg        string
			sfIndex          int
			expectedSFRep    string
			shouldFail       bool
			expectedErr      error
		}{
			{
				name:             "Local SFs only",
				uplinkPciAddress: uplinkPciAddress,
				uplink:           &repContext{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
				sfReps: []repContext{
					{Name: "eth0", PhysPortName: "pf0sf0", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth1", PhysPortName: "pf0sf1", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth2", PhysPortName: "pf0sf2", PhysSwitchID: "c2cfc60003a1420c"},
				},
				uplinkArg:     "p0",
				sfIndex:       2,
				expectedSFRep: "eth2",
				shouldFail:    false,
			},
			{
				name:             "Local SFs and External SFs, should return local SF representor",
				uplinkPciAddress: uplinkPciAddress,
				uplink:           &repContext{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
				sfReps: []repContext{
					{Name: "eth0", PhysPortName: "pf0sf0", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth1", PhysPortName: "pf0sf1", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth2", PhysPortName: "c1pf0sf2", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth3", PhysPortName: "pf0sf2", PhysSwitchID: "c2cfc60003a1420c"},
				},
				uplinkArg:     "p0",
				sfIndex:       2,
				expectedSFRep: "eth3",
				shouldFail:    false,
			},
			{
				name:             "Local SFs and External SFs, should return local SF representor - no uplink",
				uplinkPciAddress: uplinkPciAddress,
				sfReps: []repContext{
					{Name: "eth0", PhysPortName: "pf0sf0", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth1", PhysPortName: "pf0sf1", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth2", PhysPortName: "c1pf0sf2", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth3", PhysPortName: "pf0sf2", PhysSwitchID: "c2cfc60003a1420c"},
				},
				uplinkArg:     uplinkPciAddress,
				sfIndex:       2,
				expectedSFRep: "eth3",
				shouldFail:    false,
			},
			{
				name:             "SF rep no found",
				uplinkPciAddress: uplinkPciAddress,
				uplink:           &repContext{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
				sfReps: []repContext{
					{Name: "eth0", PhysPortName: "pf0sf0", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth1", PhysPortName: "pf0sf1", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth2", PhysPortName: "c1pf0sf2", PhysSwitchID: "c2cfc60003a1420c"},
				},
				uplinkArg:     "p0",
				sfIndex:       2,
				expectedSFRep: "",
				shouldFail:    true,
				expectedErr:   ErrRepresentorNotFound,
			},
			{
				name:             "SF rep no found - no uplink",
				uplinkPciAddress: uplinkPciAddress,
				sfReps: []repContext{
					{Name: "eth0", PhysPortName: "pf0sf0", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth1", PhysPortName: "pf0sf1", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth2", PhysPortName: "c1pf0sf2", PhysSwitchID: "c2cfc60003a1420c"},
				},
				uplinkArg:     uplinkPciAddress,
				sfIndex:       2,
				expectedSFRep: "",
				shouldFail:    true,
				expectedErr:   ErrRepresentorNotFound,
			},
			{
				name:             "SF rep no found no reps",
				uplinkPciAddress: uplinkPciAddress,
				uplink:           &repContext{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
				sfReps:           []repContext{},
				uplinkArg:        "p0",
				sfIndex:          2,
				expectedSFRep:    "",
				shouldFail:       true,
			},
			{
				name:             "SF rep no found only external reps",
				uplinkPciAddress: uplinkPciAddress,
				uplink:           &repContext{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
				sfReps: []repContext{
					{Name: "eth0", PhysPortName: "c1pf0sf0", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth1", PhysPortName: "c1pf0sf1", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth2", PhysPortName: "c1pf0sf2", PhysSwitchID: "c2cfc60003a1420c"},
				},
				uplinkArg:     "p0",
				sfIndex:       2,
				expectedSFRep: "",
				shouldFail:    true,
				expectedErr:   ErrRepresentorNotFound,
			},
			{
				name:             "SF rep no found sf index not found",
				uplinkPciAddress: uplinkPciAddress,
				uplink:           &repContext{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
				sfReps: []repContext{
					{Name: "eth0", PhysPortName: "c1pf0sf0", PhysSwitchID: "c2cfc60003a1420c"},
					{Name: "eth1", PhysPortName: "pf0sf1", PhysSwitchID: "c2cfc60003a1420c"},
				},
				uplinkArg:     "p0",
				sfIndex:       3,
				expectedSFRep: "",
				shouldFail:    true,
				expectedErr:   ErrRepresentorNotFound,
			},
			{
				name:             "SF rep not found - no uplink",
				uplinkPciAddress: uplinkPciAddress,
				uplink:           &repContext{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
				sfReps:           nil,
				uplinkArg:        "p1",
				sfIndex:          3,
				expectedSFRep:    "",
				shouldFail:       true,
			},
		}

		for _, tcase := range tcases {
			t.Run(tcase.name, func(t *testing.T) {
				// mock netlink calls, trigger failure to fallback to sysfs
				nlOpsMock := netlinkopsMocks.NewMockNetlinkOps(t)
				netlinkops.SetNetlinkOps(nlOpsMock)
				defer netlinkops.ResetNetlinkOps()
				nlOpsMock.On("DevLinkGetDevicePortList", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(
					nil, fmt.Errorf("failed to get devlink ports")).Maybe()

				teardown := setupRepresentorEnvForGetVfRepresentor(t, tcase.uplinkPciAddress, tcase.uplink, tcase.sfReps)
				defer teardown()
				sfRep, err := GetSfRepresentor(tcase.uplinkArg, tcase.sfIndex)
				if tcase.shouldFail {
					assert.Error(t, err)
					if tcase.expectedErr != nil {
						assert.ErrorIs(t, err, tcase.expectedErr)
					}
				} else {
					assert.NoError(t, err)
					assert.Equal(t, tcase.expectedSFRep, sfRep)
				}
			})
		}
	})

	t.Run("Get SF representor from devlink", func(t *testing.T) {
		teardown := setupRepresentorEnvForGetVfRepresentor(
			t,
			uplinkPciAddress,
			&repContext{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
			nil)
		defer teardown()

		uplinkDevlinkPort := &netlink.DevlinkPort{
			NetdeviceName:    "p0",
			PortFlavour:      uint16(PORT_FLAVOUR_PHYSICAL),
			ControllerNumber: ptrTo(uint32(0)),
			PortNumber:       ptrTo(uint32(0)),
		}

		//nolint:prealloc
		repDevlinkPorts := []*netlink.DevlinkPort{
			{
				NetdeviceName:    "c1pf0sf10",
				PortFlavour:      uint16(PORT_FLAVOUR_PCI_SF),
				ControllerNumber: ptrTo(uint32(1)),
				SfNumber:         ptrTo(uint32(10)),
			},
			{
				NetdeviceName:    "pf0vf10",
				PortFlavour:      uint16(PORT_FLAVOUR_PCI_VF),
				ControllerNumber: ptrTo(uint32(0)),
				VfNumber:         ptrTo(uint16(10)),
			},
			{
				NetdeviceName:    "pf0sf10",
				PortFlavour:      uint16(PORT_FLAVOUR_PCI_SF),
				ControllerNumber: ptrTo(uint32(0)),
				SfNumber:         ptrTo(uint32(10)),
			},
		}

		tcases := []struct {
			name             string
			uplinkPciAddress string
			uplink           *repContext
			devlinkPorts     []*netlink.DevlinkPort
			uplinkArg        string
			sfIndex          int
			expectedSFRep    string
		}{
			{
				name:             "uplink representor present",
				uplinkPciAddress: uplinkPciAddress,
				uplink:           &repContext{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
				devlinkPorts:     append(repDevlinkPorts, uplinkDevlinkPort),
				uplinkArg:        "p0",
				sfIndex:          10,
				expectedSFRep:    "pf0sf10",
			},
			{
				name:             "uplink representor not present",
				uplinkPciAddress: uplinkPciAddress,
				devlinkPorts:     repDevlinkPorts,
				uplinkArg:        uplinkPciAddress,
				sfIndex:          10,
				expectedSFRep:    "pf0sf10",
			},
		}

		for _, tcase := range tcases {
			t.Run(tcase.name, func(t *testing.T) {

				teardown := setupRepresentorEnvForGetVfRepresentor(t, uplinkPciAddress, tcase.uplink, nil)
				defer teardown()

				nlOpsMock := netlinkopsMocks.NewMockNetlinkOps(t)
				netlinkops.SetNetlinkOps(nlOpsMock)
				defer netlinkops.ResetNetlinkOps()
				nlOpsMock.On("DevLinkGetDevicePortList", "pci", tcase.uplinkPciAddress).Return(
					tcase.devlinkPorts, nil)

				sfRep, err := GetSfRepresentor(tcase.uplinkArg, tcase.sfIndex)
				assert.NoError(t, err)
				assert.Equal(t, tcase.expectedSFRep, sfRep)
			})
		}
	})
}

func TestGetPortIndexFromRepresentor(t *testing.T) {
	tcases := []struct {
		name          string
		netdev        string
		reps          []*repContext
		expectedID    int
		shouldFail    bool
		expectedError string
	}{
		{
			name:   "VF rep",
			netdev: "eth5",
			reps: []*repContext{
				{Name: "eth5", PhysPortName: "pf0vf5", PhysSwitchID: "c2cfc60003a1420c"},
			},
			expectedID:    5,
			shouldFail:    false,
			expectedError: "",
		},
		{
			name:   "SF rep",
			netdev: "eth5",
			reps: []*repContext{
				{Name: "eth5", PhysPortName: "pf0sf5", PhysSwitchID: "c2cfc60003a1420c"},
			},
			expectedID:    5,
			shouldFail:    false,
			expectedError: "",
		},
		{
			name:   "external VF rep",
			netdev: "eth5",
			reps: []*repContext{
				{Name: "eth5", PhysPortName: "c1pf0vf5", PhysSwitchID: "c2cfc60003a1420c"},
			},
			expectedID:    5,
			shouldFail:    false,
			expectedError: "",
		},
		{
			name:   "externalSF rep",
			netdev: "eth5",
			reps: []*repContext{
				{Name: "eth5", PhysPortName: "c1pf0sf5", PhysSwitchID: "c2cfc60003a1420c"},
			},
			expectedID:    5,
			shouldFail:    false,
			expectedError: "",
		},
		{
			name:   "unsupported pf rep",
			netdev: "pf0hpf",
			reps: []*repContext{
				{Name: "pf0hpf", PhysPortName: "pf0", PhysSwitchID: "c2cfc60003a1420c"},
			},
			expectedID:    0,
			shouldFail:    true,
			expectedError: "unsupported port flavor",
		},
		{
			name:   "unsupported uplink rep",
			netdev: "p0",
			reps: []*repContext{
				{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
			},
			expectedID:    0,
			shouldFail:    true,
			expectedError: "unsupported port flavor",
		},
		{
			name:   "netdev does not have phys_port_name",
			netdev: "eth5",
			reps: []*repContext{
				{Name: "eth5", PhysPortName: "", PhysSwitchID: "c2cfc60003a1420c"},
			},
			expectedID:    0,
			shouldFail:    true,
			expectedError: "no such file or directory",
		},
		{
			name:   "netdev is not a representor",
			netdev: "eth5",
			reps: []*repContext{
				{Name: "eth5", PhysPortName: "p0", PhysSwitchID: ""},
			},
			expectedID:    0,
			shouldFail:    true,
			expectedError: "does not represent an eswitch port",
		},
	}

	for _, tcase := range tcases {
		t.Run(tcase.name, func(t *testing.T) {
			teardown := setupRepresentorEnv(t, tcase.reps)
			defer teardown()

			nlOpsMock := netlinkopsMocks.NewMockNetlinkOps(t)
			netlinkops.SetNetlinkOps(nlOpsMock)
			defer netlinkops.ResetNetlinkOps()

			nlOpsMock.On("DevLinkGetPortByNetdevName", mock.AnythingOfType("string")).Return(
				nil, fmt.Errorf("failed to get devlink port")).Maybe()

			portID, err := GetPortIndexFromRepresentor(tcase.netdev)
			if tcase.shouldFail {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tcase.expectedError)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, portID, tcase.expectedID)
			}
		})
	}
}

func TestGetPortIndexFromRepresentorDevlink(t *testing.T) {
	tcases := []struct {
		name          string
		netdev        string
		reps          []*repContext
		devlinkPort   *netlink.DevlinkPort
		expectedID    int
		shouldFail    bool
		expectedError string
	}{
		{
			name:   "VF rep index from devlink",
			netdev: "eth5",
			reps: []*repContext{
				{Name: "eth5", PhysPortName: "pf0vf5", PhysSwitchID: "c2cfc60003a1420c"},
			},
			devlinkPort: &netlink.DevlinkPort{
				NetdeviceName:    "eth5",
				PortFlavour:      uint16(PORT_FLAVOUR_PCI_VF),
				ControllerNumber: ptrTo(uint32(0)),
				VfNumber:         ptrTo(uint16(5)),
			},
			expectedID: 5,
		},
		{
			name:   "SF rep index from devlink",
			netdev: "eth5",
			reps: []*repContext{
				{Name: "eth5", PhysPortName: "pf0sf5", PhysSwitchID: "c2cfc60003a1420c"},
			},
			devlinkPort: &netlink.DevlinkPort{
				NetdeviceName:    "eth5",
				PortFlavour:      uint16(PORT_FLAVOUR_PCI_SF),
				ControllerNumber: ptrTo(uint32(0)),
				SfNumber:         ptrTo(uint32(5)),
			},
			expectedID: 5,
		},
		{
			name:   "devlink takes precedence over phys_port_name",
			netdev: "eth5",
			reps: []*repContext{
				// phys_port_name says vf=99, devlink reports vf=5
				{Name: "eth5", PhysPortName: "pf0vf99", PhysSwitchID: "c2cfc60003a1420c"},
			},
			devlinkPort: &netlink.DevlinkPort{
				NetdeviceName: "eth5",
				PortFlavour:   uint16(PORT_FLAVOUR_PCI_VF),
				VfNumber:      ptrTo(uint16(5)),
			},
			expectedID: 5,
		},
		{
			name:   "VF rep with nil VfNumber falls back to sysfs",
			netdev: "eth5",
			reps: []*repContext{
				{Name: "eth5", PhysPortName: "pf0vf5", PhysSwitchID: "c2cfc60003a1420c"},
			},
			devlinkPort: &netlink.DevlinkPort{
				NetdeviceName: "eth5",
				PortFlavour:   uint16(PORT_FLAVOUR_PCI_VF),
				VfNumber:      nil,
			},
			expectedID: 5,
		},
		{
			name:   "SF rep with nil SfNumber falls back to sysfs",
			netdev: "eth5",
			reps: []*repContext{
				{Name: "eth5", PhysPortName: "pf0sf5", PhysSwitchID: "c2cfc60003a1420c"},
			},
			devlinkPort: &netlink.DevlinkPort{
				NetdeviceName: "eth5",
				PortFlavour:   uint16(PORT_FLAVOUR_PCI_SF),
				SfNumber:      nil,
			},
			expectedID: 5,
		},
	}

	for _, tcase := range tcases {
		t.Run(tcase.name, func(t *testing.T) {
			teardown := setupRepresentorEnv(t, tcase.reps)
			defer teardown()

			nlOpsMock := netlinkopsMocks.NewMockNetlinkOps(t)
			netlinkops.SetNetlinkOps(nlOpsMock)
			defer netlinkops.ResetNetlinkOps()

			nlOpsMock.On("DevLinkGetPortByNetdevName", mock.AnythingOfType("string")).Return(
				tcase.devlinkPort, nil)

			portID, err := GetPortIndexFromRepresentor(tcase.netdev)
			if tcase.shouldFail {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tcase.expectedError)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tcase.expectedID, portID)
			}
		})
	}
}

func TestGetSfRepresentorDPU(t *testing.T) {
	tcases := []struct {
		name          string
		vfReps        []*repContext
		pfID          string
		sfID          string
		expectedSFRep string
		shouldFail    bool
		expectedErr   error
	}{
		{
			name: "Host SFs only",
			vfReps: []*repContext{
				{Name: "eth0", PhysPortName: "c1pf0sf1", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "eth1", PhysPortName: "c1pf0sf2", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "eth2", PhysPortName: "c1pf0sf3", PhysSwitchID: "c2cfc60003a1420c"},
			},
			pfID:          "0",
			sfID:          "2",
			expectedSFRep: "eth1",
			shouldFail:    false,
		},
		{
			name: "Host SFs and DPU SFs",
			vfReps: []*repContext{
				{Name: "eth0", PhysPortName: "c1pf0sf0", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "eth1", PhysPortName: "pf0sf0", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "eth2", PhysPortName: "pf0sf2", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "eth3", PhysPortName: "c1pf0sf2", PhysSwitchID: "c2cfc60003a1420c"},
			},
			pfID:          "0",
			sfID:          "2",
			expectedSFRep: "eth3",
			shouldFail:    false,
		},
		{
			name: "DPU SFs only (rep names dont have controller prefix)",
			vfReps: []*repContext{
				{Name: "eth0", PhysPortName: "pf0sf0", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "eth1", PhysPortName: "pf0sf1", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "eth2", PhysPortName: "pf0sf2", PhysSwitchID: "c2cfc60003a1420c"},
			},
			pfID:          "0",
			sfID:          "2",
			expectedSFRep: "",
			shouldFail:    true,
			expectedErr:   ErrRepresentorNotFound,
		},
		{
			name: "SF representor not found",
			vfReps: []*repContext{
				{Name: "eth0", PhysPortName: "c1pf0sf0", PhysSwitchID: "c2cfc60003a1420c"},
			},
			pfID:          "0",
			sfID:          "5",
			expectedSFRep: "",
			shouldFail:    true,
			expectedErr:   ErrRepresentorNotFound,
		},
		{
			name:          "invalid pfID",
			vfReps:        []*repContext{},
			pfID:          "3",
			sfID:          "5",
			expectedSFRep: "",
			shouldFail:    true,
		},
		{
			name:          "invalid pfID - 2",
			vfReps:        []*repContext{},
			pfID:          "bla",
			sfID:          "5",
			expectedSFRep: "",
			shouldFail:    true,
		},
		{
			name:          "invalid sfID",
			vfReps:        []*repContext{},
			pfID:          "0",
			sfID:          "bla",
			expectedSFRep: "",
			shouldFail:    true,
		},
	}

	for _, tcase := range tcases {
		t.Run(tcase.name, func(t *testing.T) {
			teardown := setupRepresentorEnv(t, tcase.vfReps)
			defer teardown()
			vfRep, err := GetSfRepresentorDPU(tcase.pfID, tcase.sfID)
			if tcase.shouldFail {
				assert.Error(t, err)
				if tcase.expectedErr != nil {
					assert.ErrorIs(t, err, tcase.expectedErr)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tcase.expectedSFRep, vfRep)
			}
		})
	}
}

func TestGetVfRepresentorPortFlavour(t *testing.T) {
	tcases := []struct {
		name       string
		netdev     string
		rep        repContext
		expected   PortFlavour
		shouldFail bool
	}{
		{
			name:       "Physical flavor",
			netdev:     "eth0",
			rep:        repContext{Name: "eth0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
			expected:   PORT_FLAVOUR_PHYSICAL,
			shouldFail: false,
		},
		{
			name:       "PF flavor",
			netdev:     "eth0",
			rep:        repContext{Name: "eth0", PhysPortName: "pf0", PhysSwitchID: "c2cfc60003a1420c"},
			expected:   PORT_FLAVOUR_PCI_PF,
			shouldFail: false,
		},
		{
			name:       "VF flavor",
			netdev:     "eth0",
			rep:        repContext{Name: "eth0", PhysPortName: "pf0vf0", PhysSwitchID: "c2cfc60003a1420c"},
			expected:   PORT_FLAVOUR_PCI_VF,
			shouldFail: false,
		},
		{
			name:       "VF flavor external VF",
			netdev:     "eth0",
			rep:        repContext{Name: "eth0", PhysPortName: "c1pf0vf0", PhysSwitchID: "c2cfc60003a1420c"},
			expected:   PORT_FLAVOUR_PCI_VF,
			shouldFail: false,
		},
		{
			name:       "SF flavor",
			netdev:     "eth0",
			rep:        repContext{Name: "eth0", PhysPortName: "pf0sf0", PhysSwitchID: "c2cfc60003a1420c"},
			expected:   PORT_FLAVOUR_PCI_SF,
			shouldFail: false,
		},
		{
			name:       "SF flavor external SF",
			netdev:     "eth0",
			rep:        repContext{Name: "eth0", PhysPortName: "c1pf0sf0", PhysSwitchID: "c2cfc60003a1420c"},
			expected:   PORT_FLAVOUR_PCI_SF,
			shouldFail: false,
		},
		{
			name:       "unknown flavor - not switchdev",
			netdev:     "eth0",
			rep:        repContext{Name: "eth0", PhysPortName: "pf0vf0", PhysSwitchID: ""},
			expected:   PORT_FLAVOUR_UNKNOWN,
			shouldFail: true,
		},
		{
			name:       "unknown flavor - not phys_port_name",
			netdev:     "eth0",
			rep:        repContext{Name: "eth0", PhysPortName: "", PhysSwitchID: "c2cfc60003a1420c"},
			expected:   PORT_FLAVOUR_UNKNOWN,
			shouldFail: true,
		},
		{
			name:       "unknown flavor - invalid phys_port_name",
			netdev:     "eth0",
			rep:        repContext{Name: "eth0", PhysPortName: "invalid", PhysSwitchID: "c2cfc60003a1420c"},
			expected:   PORT_FLAVOUR_UNKNOWN,
			shouldFail: false,
		},
		{
			name:       "unknown flavor - no device",
			netdev:     "eth1",
			rep:        repContext{Name: "eth0", PhysPortName: "pf0vf34", PhysSwitchID: "c2cfc60003a1420c"},
			expected:   PORT_FLAVOUR_UNKNOWN,
			shouldFail: true,
		},
	}

	for _, tcase := range tcases {
		t.Run(tcase.name, func(t *testing.T) {
			teardown := setupRepresentorEnv(t, []*repContext{&tcase.rep})
			defer teardown()

			nlOpsMock := netlinkopsMocks.NewMockNetlinkOps(t)
			netlinkops.SetNetlinkOps(nlOpsMock)
			defer netlinkops.ResetNetlinkOps()

			nlOpsMock.On("DevLinkGetPortByNetdevName", mock.AnythingOfType("string")).Return(
				nil, fmt.Errorf("failed to get devlink port")).Maybe()

			f, err := GetRepresentorPortFlavour(tcase.netdev)
			if tcase.shouldFail {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tcase.expected, f)
			}
		})
	}
}

func TestGetVfRepresentorPortFlavourDevlink(t *testing.T) {
	nlOpsMock := netlinkopsMocks.NewMockNetlinkOps(t)
	netlinkops.SetNetlinkOps(nlOpsMock)
	defer netlinkops.ResetNetlinkOps()

	teardown := setupRepresentorEnv(t, []*repContext{{
		Name:         "enp3s0f0_0",
		PhysPortName: "pf0vf0",
		PhysSwitchID: "c2cfc60003a1420c",
	}})
	defer teardown()

	nlOpsMock.On("DevLinkGetPortByNetdevName", mock.AnythingOfType("string")).Return(
		&netlink.DevlinkPort{
			BusName:       "pci",
			DeviceName:    "0000:03:00.0",
			PortIndex:     126654,
			PortType:      2, // ETH
			NetdeviceName: "enp3s0f0_0",
			PortFlavour:   PORT_FLAVOUR_PCI_VF,
			Fn:            nil,
		}, nil)

	f, err := GetRepresentorPortFlavour("enp3s0f0_0")
	assert.NoError(t, err)
	assert.Equal(t, PortFlavour(PORT_FLAVOUR_PCI_VF), f)
}

func TestGetRepresentorPeerMacAddress(t *testing.T) {
	// Create uplink and PF representor relate files
	vfReps := []*repContext{
		{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
		{Name: "pf0hpf", PhysPortName: "pf0", PhysSwitchID: "c2cfc60003a1420c"},
		{Name: "pf0vf5", PhysPortName: "pf0vf5", PhysSwitchID: "c2cfc60003a1420c"},
		{Name: "pf0vf10", PhysPortName: "c1pf0vf10", PhysSwitchID: "c2cfc60003a1420c"},
		{Name: "pf0sf5", PhysPortName: "pf0sf5", PhysSwitchID: "c2cfc60003a1420c"},
		{Name: "pf0sf10", PhysPortName: "c1pf0sf10", PhysSwitchID: "c2cfc60003a1420c"},
	}
	teardown := setupRepresentorEnv(t, vfReps)
	defer teardown()
	defer netlinkops.ResetNetlinkOps()

	// Create PF representor config file
	repConfigFile := `
MAC        : 0c:42:a1:de:cf:7c
MaxTxRate  : 0
State      : Follow
`
	setupDPUConfigFileForPort(t, "p0", "pf", repConfigFile)

	// The uplink "p0" is now searched in the parent device net dir of "pf0hpf"
	err := utilfs.Fs.MkdirAll(filepath.Join(NetSysDir, "pf0hpf", "device", "net", "p0"), os.FileMode(0755))
	assert.NoError(t, err)

	// Run test
	tcases := []struct {
		name        string
		netdev      string
		expectedMac string
		shouldFail  bool
	}{
		{name: "PF rep", netdev: "pf0hpf", expectedMac: "0c:42:a1:de:cf:7c", shouldFail: false},
		{name: "VF rep", netdev: "pf0vf5", expectedMac: "", shouldFail: true},
		{name: "Ext VF rep", netdev: "pf0vf10", expectedMac: "", shouldFail: true},
		{name: "SF rep", netdev: "pf0sf5", expectedMac: "", shouldFail: true},
		{name: "Ext SF rep", netdev: "pf0sf10", expectedMac: "", shouldFail: true},
		{name: "Physical rep", netdev: "p0", expectedMac: "", shouldFail: true},
		{name: "Unknown rep", netdev: "foobar", expectedMac: "", shouldFail: true},
	}

	for _, tcase := range tcases {
		t.Run(tcase.name, func(t *testing.T) {
			nlOpsMock := netlinkopsMocks.NewMockNetlinkOps(t)
			netlinkops.SetNetlinkOps(nlOpsMock)
			nlOpsMock.On("DevLinkGetPortByNetdevName", mock.AnythingOfType("string")).Return(
				nil, fmt.Errorf("failed to get devlink port")).Maybe()

			mac, err := GetRepresentorPeerMacAddress(tcase.netdev)
			if tcase.shouldFail {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tcase.expectedMac, mac.String())
			}
		})
	}
}

func TestGetRepresentorPeerMacAddressDevlink(t *testing.T) {
	nlOpsMock := netlinkopsMocks.NewMockNetlinkOps(t)
	netlinkops.SetNetlinkOps(nlOpsMock)
	defer netlinkops.ResetNetlinkOps()

	teardown := setupRepresentorEnv(t, []*repContext{{
		Name:         "pf0hpf",
		PhysPortName: "pf0",
		PhysSwitchID: "c2cfc60003a1420c",
	}})
	defer teardown()

	dlport := netlink.DevlinkPort{
		BusName:       "pci",
		DeviceName:    "0000:03:00.0",
		PortIndex:     126654,
		PortType:      2, // ETH
		NetdeviceName: "pf0hpf",
		PortFlavour:   PORT_FLAVOUR_PCI_PF,
		Fn:            &netlink.DevlinkPortFn{HwAddr: net.HardwareAddr{0x0c, 0x42, 0xa1, 0xde, 0xcf, 0x7c}},
	}
	nlOpsMock.On("DevLinkGetPortByNetdevName", mock.AnythingOfType("string")).Return(&dlport, nil)
	nlOpsMock.On("DevLinkGetPortByNetdevName", mock.AnythingOfType("string")).Return(&dlport, nil)

	mac, err := GetRepresentorPeerMacAddress("pf0hpf")
	assert.NoError(t, err)
	assert.Equal(t, "0c:42:a1:de:cf:7c", mac.String())
}

func TestSetRepresentorPeerMacAddress(t *testing.T) {
	pfID := "0"
	vfIdx := "24"
	mac := net.HardwareAddr{0, 0, 0, 1, 2, 3}

	tcases := []struct {
		name        string
		netdev      string
		reps        []*repContext
		expectedMac string
		shouldFail  bool
	}{
		{
			name:   "VF rep with external controller",
			netdev: "pf0vf24",
			reps: []*repContext{
				{Name: "pf0vf24", PhysPortName: "c1pf0vf24", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
			},
			expectedMac: mac.String(),
			shouldFail:  false,
		},
		{
			name:   "VF rep without external controller - Legacy",
			netdev: "pf0vf24",
			reps: []*repContext{
				{Name: "pf0vf24", PhysPortName: "pf0vf24", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
			},
			expectedMac: mac.String(),
			shouldFail:  false,
		},
		{
			name:   "PF rep should fail",
			netdev: "pf0hpf",
			reps: []*repContext{
				{Name: "pf0hpf", PhysPortName: "pf0", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
			},
			expectedMac: "",
			shouldFail:  true,
		},
		{
			name:   "non existent representor should fail",
			netdev: "foobar",
			reps: []*repContext{
				{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
			},
			expectedMac: "",
			shouldFail:  true,
		},
	}

	for _, tcase := range tcases {
		t.Run(tcase.name, func(t *testing.T) {
			teardown := setupRepresentorEnv(t, tcase.reps)
			defer teardown()

			nlOpsMock := netlinkopsMocks.NewMockNetlinkOps(t)
			netlinkops.SetNetlinkOps(nlOpsMock)
			defer netlinkops.ResetNetlinkOps()

			nlOpsMock.On("DevLinkGetPortByNetdevName", mock.AnythingOfType("string")).
				Return(nil, fmt.Errorf("no devlink support")).Maybe()

			//  setup sysfs layout
			path := fmt.Sprintf("%s/p%s/smart_nic/vf%s", NetSysDir, pfID, vfIdx)
			_ = utilfs.Fs.MkdirAll(path, os.FileMode(0755))

			macFile := filepath.Join(path, "mac")
			_, err := utilfs.Fs.Create(macFile)
			assert.NoError(t, err)

			// Create parent device net dir so the uplink can be found via the sysfs fallback path
			parentNetDir := filepath.Join(NetSysDir, tcase.netdev, "device", "net")
			err = utilfs.Fs.MkdirAll(parentNetDir, os.FileMode(0755))
			assert.NoError(t, err)
			// create representors in the parent net dir
			for _, rep := range tcase.reps {
				err = utilfs.Fs.MkdirAll(filepath.Join(parentNetDir, rep.Name), os.FileMode(0755))
				assert.NoError(t, err)
			}

			// execute test
			err = SetRepresentorPeerMacAddress(tcase.netdev, mac)
			if tcase.shouldFail {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				// verify mac file content
				content, err := utilfs.Fs.ReadFile(macFile)
				assert.NoError(t, err)
				assert.Equal(t, mac.String(), string(content))
			}
		})
	}
}

func TestSetRepresentorPeerMacAddressDevlink(t *testing.T) {
	mac := net.HardwareAddr{0, 0, 0, 1, 2, 3}

	dlport := &netlink.DevlinkPort{
		BusName:     "pci",
		DeviceName:  "0000:03:00.0",
		PortIndex:   1,
		PortFlavour: uint16(PORT_FLAVOUR_PCI_VF),
	}

	fnAttrs := netlink.DevlinkPortFnSetAttrs{
		FnAttrs:     netlink.DevlinkPortFn{HwAddr: mac},
		HwAddrValid: true,
	}

	tcases := []struct {
		name            string
		netdev          string
		reps            []*repContext
		macFile         string // sysfs mac file path to create; empty means no sysfs setup
		devlinkFnSetErr error
		expectedMac     string
		shouldFail      bool
	}{
		{
			name:   "devlink set succeeds",
			netdev: "pf0vf5",
			reps: []*repContext{
				{Name: "pf0vf5", PhysPortName: "pf0vf5", PhysSwitchID: "c2cfc60003a1420c"},
			},
			macFile:         "",
			devlinkFnSetErr: nil,
			shouldFail:      false,
		},
		{
			name:   "devlink set fails fallback to sysfs",
			netdev: "pf0vf5",
			reps: []*repContext{
				{Name: "pf0vf5", PhysPortName: "pf0vf5", PhysSwitchID: "c2cfc60003a1420c"},
				{Name: "p0", PhysPortName: "p0", PhysSwitchID: "c2cfc60003a1420c"},
			},
			macFile:         filepath.Join(NetSysDir, "p0", "smart_nic", "vf5", "mac"),
			devlinkFnSetErr: fmt.Errorf("devlink set failed"),
			expectedMac:     mac.String(),
			shouldFail:      false,
		},
	}

	for _, tcase := range tcases {
		t.Run(tcase.name, func(t *testing.T) {
			teardown := setupRepresentorEnv(t, tcase.reps)
			defer teardown()

			nlOpsMock := netlinkopsMocks.NewMockNetlinkOps(t)
			netlinkops.SetNetlinkOps(nlOpsMock)
			defer netlinkops.ResetNetlinkOps()

			if tcase.macFile != "" {
				// setup sysfs mac file and parent device net dir for uplink lookup
				err := utilfs.Fs.MkdirAll(filepath.Dir(tcase.macFile), os.FileMode(0755))
				assert.NoError(t, err)
				_, err = utilfs.Fs.Create(tcase.macFile)
				assert.NoError(t, err)

				parentNetDir := filepath.Join(NetSysDir, tcase.netdev, "device", "net")
				err = utilfs.Fs.MkdirAll(parentNetDir, os.FileMode(0755))
				assert.NoError(t, err)
				for _, rep := range tcase.reps {
					err = utilfs.Fs.MkdirAll(filepath.Join(parentNetDir, rep.Name), os.FileMode(0755))
					assert.NoError(t, err)
				}
			}

			nlOpsMock.On("DevLinkGetPortByNetdevName", tcase.netdev).Return(dlport, nil)
			nlOpsMock.On("DevLinkPortFnSet", dlport.BusName, dlport.DeviceName, dlport.PortIndex, fnAttrs).
				Return(tcase.devlinkFnSetErr)

			err := SetRepresentorPeerMacAddress(tcase.netdev, mac)
			if tcase.shouldFail {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tcase.macFile != "" {
					content, err := utilfs.Fs.ReadFile(tcase.macFile)
					assert.NoError(t, err)
					assert.Equal(t, tcase.expectedMac, string(content))
				}
			}
		})
	}
}
