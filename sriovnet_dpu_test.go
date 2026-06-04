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

package sriovnet

import (
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vishvananda/netlink"

	"github.com/k8snetworkplumbingwg/sriovnet/pkg/utils/docacaps"
	docacapsMocks "github.com/k8snetworkplumbingwg/sriovnet/pkg/utils/docacaps/mocks"
	"github.com/k8snetworkplumbingwg/sriovnet/pkg/utils/netlinkops"
	netlinkopsMocks "github.com/k8snetworkplumbingwg/sriovnet/pkg/utils/netlinkops/mocks"
)

func TestGetVfRepresentorFromPortParams(t *testing.T) {
	const ecpf = "0000:03:00.0"

	tcases := []struct {
		name          string
		pp            *RepresentorPortParams
		vfIndex       uint32
		devlinkPorts  []*netlink.DevlinkPort
		devlinkErr    error
		expectedRep   string
		shouldFail    bool
		expectedError string
	}{
		{
			name:          "nil port params",
			pp:            nil,
			vfIndex:       0,
			shouldFail:    true,
			expectedError: "port parameters are nil",
		},
		{
			name:    "VF representor found - controller 0",
			pp:      &RepresentorPortParams{ECPF: ecpf, ControllerNumber: 0, PFNumber: 0},
			vfIndex: 1,
			devlinkPorts: []*netlink.DevlinkPort{
				{
					NetdeviceName:    "pf0vf0",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_VF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
					VfNumber:         ptrTo(uint16(0)),
				},
				{
					NetdeviceName:    "pf0vf1",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_VF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
					VfNumber:         ptrTo(uint16(1)),
				},
			},
			expectedRep: "pf0vf1",
		},
		{
			name:    "VF representor found - external controller",
			pp:      &RepresentorPortParams{ECPF: ecpf, ControllerNumber: 1, PFNumber: 0},
			vfIndex: 2,
			devlinkPorts: []*netlink.DevlinkPort{
				{
					NetdeviceName:    "pf0vf2",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_VF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
					VfNumber:         ptrTo(uint16(2)),
				},
				{
					NetdeviceName:    "c1pf0vf2",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_VF),
					ControllerNumber: ptrTo(uint32(1)),
					PfNumber:         ptrTo(uint16(0)),
					VfNumber:         ptrTo(uint16(2)),
				},
			},
			expectedRep: "c1pf0vf2",
		},
		{
			name:          "devlink returns error",
			pp:            &RepresentorPortParams{ECPF: ecpf, ControllerNumber: 0, PFNumber: 0},
			vfIndex:       0,
			devlinkErr:    fmt.Errorf("devlink error"),
			expectedRep:   "",
			shouldFail:    true,
			expectedError: "failed to get representor netdev name for VF 0",
		},
		{
			name:          "VF not found - empty list",
			pp:            &RepresentorPortParams{ECPF: ecpf, ControllerNumber: 0, PFNumber: 0},
			vfIndex:       0,
			devlinkPorts:  []*netlink.DevlinkPort{},
			expectedRep:   "",
			shouldFail:    true,
			expectedError: "failed to get representor netdev name for VF 0",
		},
		{
			name:    "VF not found - matching VF index but different PF",
			pp:      &RepresentorPortParams{ECPF: ecpf, ControllerNumber: 0, PFNumber: 0},
			vfIndex: 0,
			devlinkPorts: []*netlink.DevlinkPort{
				{
					NetdeviceName:    "pf1vf0",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_VF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(1)),
					VfNumber:         ptrTo(uint16(0)),
				},
			},
			shouldFail: true,
		},
		{
			name:    "VF not found - matching VF index but different controller",
			pp:      &RepresentorPortParams{ECPF: ecpf, ControllerNumber: 0, PFNumber: 0},
			vfIndex: 0,
			devlinkPorts: []*netlink.DevlinkPort{
				{
					NetdeviceName:    "c1pf0vf0",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_VF),
					ControllerNumber: ptrTo(uint32(1)),
					PfNumber:         ptrTo(uint16(0)),
					VfNumber:         ptrTo(uint16(0)),
				},
			},
			shouldFail: true,
		},
		{
			name:    "VF not found - only SF/PF ports",
			pp:      &RepresentorPortParams{ECPF: ecpf, ControllerNumber: 0, PFNumber: 0},
			vfIndex: 0,
			devlinkPorts: []*netlink.DevlinkPort{
				{
					NetdeviceName:    "pf0sf0",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_SF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
					SfNumber:         ptrTo(uint32(0)),
				},
				{
					NetdeviceName:    "pf0",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_PF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
				},
			},
			shouldFail: true,
		},
		{
			name:    "VF port with nil VfNumber returns error",
			pp:      &RepresentorPortParams{ECPF: ecpf, ControllerNumber: 0, PFNumber: 0},
			vfIndex: 0,
			devlinkPorts: []*netlink.DevlinkPort{
				{
					NetdeviceName:    "pf0vf0",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_VF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
					VfNumber:         nil,
				},
			},
			shouldFail: true,
		},
		{
			name:    "VF port found but empty NetdeviceName",
			pp:      &RepresentorPortParams{ECPF: ecpf, ControllerNumber: 0, PFNumber: 0},
			vfIndex: 0,
			devlinkPorts: []*netlink.DevlinkPort{
				{
					NetdeviceName:    "",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_VF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
					VfNumber:         ptrTo(uint16(0)),
				},
			},
			shouldFail: true,
		},
	}

	for _, tcase := range tcases {
		t.Run(tcase.name, func(t *testing.T) {
			nlOpsMock := netlinkopsMocks.NewMockNetlinkOps(t)
			netlinkops.SetNetlinkOps(nlOpsMock)
			defer netlinkops.ResetNetlinkOps()

			if tcase.pp != nil {
				nlOpsMock.On("DevLinkGetDevicePortList", "pci", tcase.pp.ECPF).Return(
					tcase.devlinkPorts, tcase.devlinkErr)
			}

			rep, err := GetVfRepresentorFromPortParams(tcase.pp, tcase.vfIndex)
			if tcase.shouldFail {
				assert.Error(t, err)
				if tcase.expectedError != "" {
					assert.Contains(t, err.Error(), tcase.expectedError)
				}
				assert.Equal(t, "", rep)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tcase.expectedRep, rep)
			}
		})
	}
}

func TestGetSfRepresentorFromPortParams(t *testing.T) {
	const ecpf = "0000:03:00.0"

	tcases := []struct {
		name          string
		pp            *RepresentorPortParams
		sfIndex       uint32
		devlinkPorts  []*netlink.DevlinkPort
		devlinkErr    error
		expectedRep   string
		shouldFail    bool
		expectedError string
	}{
		{
			name:          "nil port params",
			pp:            nil,
			sfIndex:       0,
			shouldFail:    true,
			expectedError: "port parameters are nil",
		},
		{
			name:    "SF representor found - controller 0",
			pp:      &RepresentorPortParams{ECPF: ecpf, ControllerNumber: 0, PFNumber: 0},
			sfIndex: 5,
			devlinkPorts: []*netlink.DevlinkPort{
				{
					NetdeviceName:    "pf0sf0",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_SF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
					SfNumber:         ptrTo(uint32(0)),
				},
				{
					NetdeviceName:    "pf0sf5",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_SF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
					SfNumber:         ptrTo(uint32(5)),
				},
			},
			expectedRep: "pf0sf5",
		},
		{
			name:    "SF representor found - external controller",
			pp:      &RepresentorPortParams{ECPF: ecpf, ControllerNumber: 1, PFNumber: 0},
			sfIndex: 10,
			devlinkPorts: []*netlink.DevlinkPort{
				{
					NetdeviceName:    "pf0sf10",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_SF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
					SfNumber:         ptrTo(uint32(10)),
				},
				{
					NetdeviceName:    "c1pf0sf10",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_SF),
					ControllerNumber: ptrTo(uint32(1)),
					PfNumber:         ptrTo(uint16(0)),
					SfNumber:         ptrTo(uint32(10)),
				},
			},
			expectedRep: "c1pf0sf10",
		},
		{
			name:          "devlink returns error",
			pp:            &RepresentorPortParams{ECPF: ecpf, ControllerNumber: 0, PFNumber: 0},
			sfIndex:       0,
			devlinkErr:    fmt.Errorf("devlink error"),
			shouldFail:    true,
			expectedError: "failed to get representor netdev name for SF 0",
		},
		{
			name:          "SF not found - empty list",
			pp:            &RepresentorPortParams{ECPF: ecpf, ControllerNumber: 0, PFNumber: 0},
			sfIndex:       0,
			devlinkPorts:  []*netlink.DevlinkPort{},
			shouldFail:    true,
			expectedError: "failed to get representor netdev name for SF 0",
		},
		{
			name:    "SF not found - matching SF index but different PF",
			pp:      &RepresentorPortParams{ECPF: ecpf, ControllerNumber: 0, PFNumber: 0},
			sfIndex: 5,
			devlinkPorts: []*netlink.DevlinkPort{
				{
					NetdeviceName:    "pf1sf5",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_SF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(1)),
					SfNumber:         ptrTo(uint32(5)),
				},
			},
			shouldFail: true,
		},
		{
			name:    "SF not found - only VF/PF ports",
			pp:      &RepresentorPortParams{ECPF: ecpf, ControllerNumber: 0, PFNumber: 0},
			sfIndex: 0,
			devlinkPorts: []*netlink.DevlinkPort{
				{
					NetdeviceName:    "pf0vf0",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_VF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
					VfNumber:         ptrTo(uint16(0)),
				},
			},
			shouldFail: true,
		},
		{
			name:    "SF port with nil SfNumber returns error",
			pp:      &RepresentorPortParams{ECPF: ecpf, ControllerNumber: 0, PFNumber: 0},
			sfIndex: 0,
			devlinkPorts: []*netlink.DevlinkPort{
				{
					NetdeviceName:    "pf0sf0",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_SF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
					SfNumber:         nil,
				},
			},
			shouldFail: true,
		},
	}

	for _, tcase := range tcases {
		t.Run(tcase.name, func(t *testing.T) {
			nlOpsMock := netlinkopsMocks.NewMockNetlinkOps(t)
			netlinkops.SetNetlinkOps(nlOpsMock)
			defer netlinkops.ResetNetlinkOps()

			if tcase.pp != nil {
				nlOpsMock.On("DevLinkGetDevicePortList", "pci", tcase.pp.ECPF).Return(
					tcase.devlinkPorts, tcase.devlinkErr)
			}

			rep, err := GetSfRepresentorFromPortParams(tcase.pp, tcase.sfIndex)
			if tcase.shouldFail {
				assert.Error(t, err)
				if tcase.expectedError != "" {
					assert.Contains(t, err.Error(), tcase.expectedError)
				}
				assert.Equal(t, "", rep)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tcase.expectedRep, rep)
			}
		})
	}
}

func TestGetPfRepresentorFromPortParams(t *testing.T) {
	const ecpf = "0000:03:00.0"

	tcases := []struct {
		name          string
		pp            *RepresentorPortParams
		devlinkPorts  []*netlink.DevlinkPort
		devlinkErr    error
		expectedRep   string
		shouldFail    bool
		expectedError string
	}{
		{
			name:          "nil port params",
			pp:            nil,
			shouldFail:    true,
			expectedError: "port parameters are nil",
		},
		{
			name: "PF representor found - controller 0 PF 0",
			pp:   &RepresentorPortParams{ECPF: ecpf, ControllerNumber: 0, PFNumber: 0},
			devlinkPorts: []*netlink.DevlinkPort{
				{
					NetdeviceName:    "pf0hpf",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_PF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
				},
				{
					NetdeviceName:    "pf1hpf",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_PF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(1)),
				},
			},
			expectedRep: "pf0hpf",
		},
		{
			name: "PF representor found - external controller",
			pp:   &RepresentorPortParams{ECPF: ecpf, ControllerNumber: 1, PFNumber: 1},
			devlinkPorts: []*netlink.DevlinkPort{
				{
					NetdeviceName:    "pf1hpf",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_PF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(1)),
				},
				{
					NetdeviceName:    "c1pf1",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_PF),
					ControllerNumber: ptrTo(uint32(1)),
					PfNumber:         ptrTo(uint16(1)),
				},
			},
			expectedRep: "c1pf1",
		},
		{
			name:          "devlink returns error",
			pp:            &RepresentorPortParams{ECPF: ecpf, ControllerNumber: 0, PFNumber: 0},
			devlinkErr:    fmt.Errorf("devlink error"),
			shouldFail:    true,
			expectedError: "failed to get representor netdev name for PF 0",
		},
		{
			name:          "PF not found - empty list",
			pp:            &RepresentorPortParams{ECPF: ecpf, ControllerNumber: 0, PFNumber: 0},
			devlinkPorts:  []*netlink.DevlinkPort{},
			shouldFail:    true,
			expectedError: "failed to get representor netdev name for PF 0",
		},
		{
			name: "PF not found - different controller",
			pp:   &RepresentorPortParams{ECPF: ecpf, ControllerNumber: 0, PFNumber: 0},
			devlinkPorts: []*netlink.DevlinkPort{
				{
					NetdeviceName:    "c1pf0",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_PF),
					ControllerNumber: ptrTo(uint32(1)),
					PfNumber:         ptrTo(uint16(0)),
				},
			},
			shouldFail: true,
		},
		{
			name: "PF not found - only VF/SF ports",
			pp:   &RepresentorPortParams{ECPF: ecpf, ControllerNumber: 0, PFNumber: 0},
			devlinkPorts: []*netlink.DevlinkPort{
				{
					NetdeviceName:    "pf0vf0",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_VF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
					VfNumber:         ptrTo(uint16(0)),
				},
			},
			shouldFail: true,
		},
		{
			name: "PF port with nil PfNumber returns error",
			pp:   &RepresentorPortParams{ECPF: ecpf, ControllerNumber: 0, PFNumber: 0},
			devlinkPorts: []*netlink.DevlinkPort{
				{
					NetdeviceName:    "pf0hpf",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_PF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         nil,
				},
			},
			shouldFail: true,
		},
	}

	for _, tcase := range tcases {
		t.Run(tcase.name, func(t *testing.T) {
			nlOpsMock := netlinkopsMocks.NewMockNetlinkOps(t)
			netlinkops.SetNetlinkOps(nlOpsMock)
			defer netlinkops.ResetNetlinkOps()

			if tcase.pp != nil {
				nlOpsMock.On("DevLinkGetDevicePortList", "pci", tcase.pp.ECPF).Return(
					tcase.devlinkPorts, tcase.devlinkErr)
			}

			rep, err := GetPfRepresentorFromPortParams(tcase.pp)
			if tcase.shouldFail {
				assert.Error(t, err)
				if tcase.expectedError != "" {
					assert.Contains(t, err.Error(), tcase.expectedError)
				}
				assert.Equal(t, "", rep)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tcase.expectedRep, rep)
			}
		})
	}
}

func TestGetPFRepresentorPortParamsFromMAC(t *testing.T) {
	mac1, err := net.ParseMAC("0c:42:a1:de:cf:7c")
	assert.NoError(t, err)
	mac2, err := net.ParseMAC("0c:42:a1:de:cf:7d")
	assert.NoError(t, err)
	otherMac, err := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	assert.NoError(t, err)

	tcases := []struct {
		name          string
		mac           net.HardwareAddr
		devlinkPorts  []*netlink.DevlinkPort
		devlinkErr    error
		expectedPP    *RepresentorPortParams
		shouldFail    bool
		expectedError string
	}{
		{
			name:          "devlink returns error",
			mac:           mac1,
			devlinkErr:    fmt.Errorf("devlink not available"),
			shouldFail:    true,
			expectedError: "failed to list devlink ports",
		},
		{
			name:          "no matching port",
			mac:           otherMac,
			devlinkPorts:  []*netlink.DevlinkPort{},
			shouldFail:    true,
			expectedError: "no matching devlink port found",
		},
		{
			name: "single matching PF port",
			mac:  mac1,
			devlinkPorts: []*netlink.DevlinkPort{
				{
					BusName:          "pci",
					DeviceName:       "0000:03:00.0",
					NetdeviceName:    "pf0hpf",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_PF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
					Fn:               &netlink.DevlinkPortFn{HwAddr: mac1},
				},
			},
			expectedPP: &RepresentorPortParams{
				ECPF:             "0000:03:00.0",
				ControllerNumber: 0,
				PFNumber:         0,
			},
		},
		{
			name: "multiple matching PF ports",
			mac:  mac1,
			devlinkPorts: []*netlink.DevlinkPort{
				{
					BusName:          "pci",
					DeviceName:       "0000:03:00.0",
					NetdeviceName:    "pf0hpf",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_PF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
					Fn:               &netlink.DevlinkPortFn{HwAddr: mac1},
				},
				{
					BusName:          "pci",
					DeviceName:       "0000:04:00.0",
					NetdeviceName:    "pf0hpf",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_PF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
					Fn:               &netlink.DevlinkPortFn{HwAddr: mac1},
				},
			},
			shouldFail:    true,
			expectedError: "multiple matching",
		},
		{
			name: "ignores non-pci ports",
			mac:  mac1,
			devlinkPorts: []*netlink.DevlinkPort{
				{
					BusName:          "auxiliary",
					DeviceName:       "mlx5_core.eth.5",
					NetdeviceName:    "pf0hpf",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_PF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
					Fn:               &netlink.DevlinkPortFn{HwAddr: mac1},
				},
			},
			shouldFail:    true,
			expectedError: "no matching devlink port found",
		},
		{
			name: "ignores non-PF flavour",
			mac:  mac1,
			devlinkPorts: []*netlink.DevlinkPort{
				{
					BusName:          "pci",
					DeviceName:       "0000:03:00.0",
					NetdeviceName:    "pf0vf0",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_VF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
					VfNumber:         ptrTo(uint16(0)),
					Fn:               &netlink.DevlinkPortFn{HwAddr: mac1},
				},
				{
					BusName:          "pci",
					DeviceName:       "0000:03:00.0",
					NetdeviceName:    "pf0sf0",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_SF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
					SfNumber:         ptrTo(uint32(0)),
					Fn:               &netlink.DevlinkPortFn{HwAddr: mac1},
				},
				{
					BusName:          "pci",
					DeviceName:       "0000:03:00.0",
					NetdeviceName:    "p0",
					PortFlavour:      uint16(PORT_FLAVOUR_PHYSICAL),
					ControllerNumber: ptrTo(uint32(0)),
					Fn:               &netlink.DevlinkPortFn{HwAddr: mac1},
				},
			},
			shouldFail:    true,
			expectedError: "no matching devlink port found",
		},
		{
			name: "skips port with nil Fn",
			mac:  mac1,
			devlinkPorts: []*netlink.DevlinkPort{
				{
					BusName:          "pci",
					DeviceName:       "0000:03:00.0",
					NetdeviceName:    "pf0hpf",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_PF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
					Fn:               nil,
				},
			},
			shouldFail:    true,
			expectedError: "no matching devlink port found",
		},
		{
			name: "skips port with different MAC",
			mac:  mac1,
			devlinkPorts: []*netlink.DevlinkPort{
				{
					BusName:          "pci",
					DeviceName:       "0000:03:00.0",
					NetdeviceName:    "pf0hpf",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_PF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
					Fn:               &netlink.DevlinkPortFn{HwAddr: mac2},
				},
			},
			shouldFail:    true,
			expectedError: "no matching devlink port found",
		},
		{
			name: "matching port missing DeviceName",
			mac:  mac1,
			devlinkPorts: []*netlink.DevlinkPort{
				{
					BusName:          "pci",
					DeviceName:       "",
					NetdeviceName:    "pf0hpf",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_PF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
					Fn:               &netlink.DevlinkPortFn{HwAddr: mac1},
				},
			},
			shouldFail:    true,
			expectedError: "missing attributes",
		},
		{
			name: "matching port missing ControllerNumber",
			mac:  mac1,
			devlinkPorts: []*netlink.DevlinkPort{
				{
					BusName:          "pci",
					DeviceName:       "0000:03:00.0",
					NetdeviceName:    "pf0hpf",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_PF),
					ControllerNumber: nil,
					PfNumber:         ptrTo(uint16(0)),
					Fn:               &netlink.DevlinkPortFn{HwAddr: mac1},
				},
			},
			shouldFail:    true,
			expectedError: "missing attributes",
		},
		{
			name: "matching port missing PfNumber",
			mac:  mac1,
			devlinkPorts: []*netlink.DevlinkPort{
				{
					BusName:          "pci",
					DeviceName:       "0000:03:00.0",
					NetdeviceName:    "pf0hpf",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_PF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         nil,
					Fn:               &netlink.DevlinkPortFn{HwAddr: mac1},
				},
			},
			shouldFail:    true,
			expectedError: "missing attributes",
		},
		{
			name: "matching port - external controller",
			mac:  mac1,
			devlinkPorts: []*netlink.DevlinkPort{
				{
					BusName:          "pci",
					DeviceName:       "0000:03:00.0",
					NetdeviceName:    "pf0hpf",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_PF),
					ControllerNumber: ptrTo(uint32(0)),
					PfNumber:         ptrTo(uint16(0)),
					Fn:               &netlink.DevlinkPortFn{HwAddr: mac2},
				},
				{
					BusName:          "pci",
					DeviceName:       "0000:03:00.0",
					NetdeviceName:    "c1pf1",
					PortFlavour:      uint16(PORT_FLAVOUR_PCI_PF),
					ControllerNumber: ptrTo(uint32(1)),
					PfNumber:         ptrTo(uint16(1)),
					Fn:               &netlink.DevlinkPortFn{HwAddr: mac1},
				},
			},
			expectedPP: &RepresentorPortParams{
				ECPF:             "0000:03:00.0",
				ControllerNumber: 1,
				PFNumber:         1,
			},
		},
	}

	for _, tcase := range tcases {
		t.Run(tcase.name, func(t *testing.T) {
			nlOpsMock := netlinkopsMocks.NewMockNetlinkOps(t)
			netlinkops.SetNetlinkOps(nlOpsMock)
			defer netlinkops.ResetNetlinkOps()

			nlOpsMock.On("DevLinkGetAllPortList").Return(tcase.devlinkPorts, tcase.devlinkErr)

			pp, err := GetPFRepresentorPortParamsFromMAC(tcase.mac)
			if tcase.shouldFail {
				assert.Error(t, err)
				if tcase.expectedError != "" {
					assert.Contains(t, err.Error(), tcase.expectedError)
				}
				assert.Nil(t, pp)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tcase.expectedPP, pp)
			}
		})
	}
}

// setMockDocaCaps overrides docacaps.NewDocaCaps for the duration of the test so that
// GetRepresentorPortParamsFromVUID uses the supplied mock instead of executing doca_caps.
// The original NewDocaCaps is restored via t.Cleanup.
func setMockDocaCaps(t *testing.T, mock *docacapsMocks.MockDOCACaps) {
	t.Helper()
	orig := docacaps.NewDocaCaps
	docacaps.NewDocaCaps = func() docacaps.DOCACaps { return mock }
	t.Cleanup(func() {
		docacaps.NewDocaCaps = orig
	})
}

func TestGetRepresentorPortParamsFromVUID(t *testing.T) {
	const (
		vuid = "e4092a71f9c1f0118000b45cb5355194MLNXS0D0F0"
		ecpf = "0000:01:00.0"
	)

	tcases := []struct {
		name          string
		vuid          string
		repDev        *docacaps.DocaCapRepDev
		repDevErr     error
		expectedPP    *RepresentorPortParams
		shouldFail    bool
		expectedError string
	}{
		{
			name: "successful lookup",
			vuid: vuid,
			repDev: &docacaps.DocaCapRepDev{
				ECPFPCIAddress:        ecpf,
				RepresentorPCIAddress: "0000:9f:00.0",
				Attributes: map[string]string{
					"host_index": "1",
					"pf_index":   "0",
					"vuid":       vuid,
				},
			},
			expectedPP: &RepresentorPortParams{
				ECPF:             ecpf,
				ControllerNumber: 1,
				PFNumber:         0,
			},
		},
		{
			name:          "doca_caps lookup error",
			vuid:          vuid,
			repDevErr:     fmt.Errorf("doca_caps failed"),
			shouldFail:    true,
			expectedError: "failed to get rep dev from doca_caps by VUID",
		},
		{
			name: "missing ECPF PCI address",
			vuid: vuid,
			repDev: &docacaps.DocaCapRepDev{
				ECPFPCIAddress: "",
				Attributes: map[string]string{
					"host_index": "1",
					"pf_index":   "0",
				},
			},
			shouldFail:    true,
			expectedError: "missing ecpf PCI address",
		},
		{
			name: "missing host_index attribute",
			vuid: vuid,
			repDev: &docacaps.DocaCapRepDev{
				ECPFPCIAddress: ecpf,
				Attributes: map[string]string{
					"pf_index": "0",
				},
			},
			shouldFail:    true,
			expectedError: "missing host_index attribute",
		},
		{
			name: "invalid host_index - non numeric",
			vuid: vuid,
			repDev: &docacaps.DocaCapRepDev{
				ECPFPCIAddress: ecpf,
				Attributes: map[string]string{
					"host_index": "notanumber",
					"pf_index":   "0",
				},
			},
			shouldFail:    true,
			expectedError: "invalid host_index attribute",
		},
		{
			name: "missing pf_index attribute",
			vuid: vuid,
			repDev: &docacaps.DocaCapRepDev{
				ECPFPCIAddress: ecpf,
				Attributes: map[string]string{
					"host_index": "0",
				},
			},
			shouldFail:    true,
			expectedError: "missing pf_index attribute",
		},
		{
			name: "invalid pf_index - non numeric",
			vuid: vuid,
			repDev: &docacaps.DocaCapRepDev{
				ECPFPCIAddress: ecpf,
				Attributes: map[string]string{
					"host_index": "0",
					"pf_index":   "notanumber",
				},
			},
			shouldFail:    true,
			expectedError: "invalid pf_index attribute",
		},
	}

	for _, tcase := range tcases {
		t.Run(tcase.name, func(t *testing.T) {
			mockDC := docacapsMocks.NewMockDOCACaps(t)
			setMockDocaCaps(t, mockDC)

			mockDC.EXPECT().GetDocaCapRepDevByVUID(tcase.vuid).Return(tcase.repDev, tcase.repDevErr)

			pp, err := GetRepresentorPortParamsFromVUID(tcase.vuid)

			if tcase.shouldFail {
				assert.Error(t, err)
				if tcase.expectedError != "" {
					assert.Contains(t, err.Error(), tcase.expectedError)
				}
				assert.Nil(t, pp)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tcase.expectedPP, pp)
			}
		})
	}
}
