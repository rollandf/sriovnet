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

	"github.com/vishvananda/netlink"

	"github.com/k8snetworkplumbingwg/sriovnet/pkg/utils/netlinkops"
)

// RepresentorPortParams contains the base port parameters for locating representors.
type RepresentorPortParams struct {
	// The PCI device address on which the representor is anchored
	ECPF string
	// The controller number
	ControllerNumber uint32
	// The PF number
	PFNumber uint16
}

// GetVfRepresentorFromPortParams returns the VF representor netdev name for a given port parameters and VF index
func GetVfRepresentorFromPortParams(pp *RepresentorPortParams, vfIndex uint32) (string, error) {
	if pp == nil {
		return "", fmt.Errorf("port parameters are nil")
	}

	rep, err := getRepresentorDevlink(pp.ECPF, PORT_FLAVOUR_PCI_VF, pp.ControllerNumber, vfIndex, &pp.PFNumber)
	if err != nil {
		return "", fmt.Errorf("failed to get representor netdev name for VF %d: %w", vfIndex, err)
	}
	return rep, nil
}

// GetSfRepresentorFromPortParams returns the SF representor netdev name for a given port parameters and SF index
func GetSfRepresentorFromPortParams(pp *RepresentorPortParams, sfIndex uint32) (string, error) {
	if pp == nil {
		return "", fmt.Errorf("port parameters are nil")
	}

	rep, err := getRepresentorDevlink(pp.ECPF, PORT_FLAVOUR_PCI_SF, pp.ControllerNumber, sfIndex, &pp.PFNumber)
	if err != nil {
		return "", fmt.Errorf("failed to get representor netdev name for SF %d: %w", sfIndex, err)
	}
	return rep, nil
}

// GetPfRepresentorFromPortParams returns the PF representor netdev name for a given port parameters
func GetPfRepresentorFromPortParams(pp *RepresentorPortParams) (string, error) {
	if pp == nil {
		return "", fmt.Errorf("port parameters are nil")
	}

	rep, err := getRepresentorDevlink(pp.ECPF, PORT_FLAVOUR_PCI_PF, pp.ControllerNumber, uint32(pp.PFNumber), &pp.PFNumber)
	if err != nil {
		return "", fmt.Errorf("failed to get representor netdev name for PF %d: %w", pp.PFNumber, err)
	}
	return rep, nil
}

// GetPFRepresentorPortParamsFromMAC returns the representor port parameters from the provided MAC address.
//
// Note: This function will work properly only when MAC addresses are unique in the system.
// If multiple ports have the same MAC address, the function will return error.
func GetPFRepresentorPortParamsFromMAC(mac net.HardwareAddr) (*RepresentorPortParams, error) {
	macStr := mac.String()

	if macStr == "" {
		return nil, fmt.Errorf("invalid MAC address %s", macStr)
	}

	// list all devlink ports
	ports, err := netlinkops.GetNetlinkOps().DevLinkGetAllPortList()
	if err != nil {
		return nil, fmt.Errorf("failed to list devlink ports: %w", err)
	}

	// find the port with the given MAC address
	var foundPorts []*netlink.DevlinkPort
	for _, port := range ports {
		if port.BusName != "pci" {
			continue
		}

		if port.PortFlavour != uint16(PORT_FLAVOUR_PCI_PF) {
			continue
		}

		if port.Fn != nil && port.Fn.HwAddr.String() == macStr {
			foundPorts = append(foundPorts, port)
		}
	}

	if len(foundPorts) == 0 {
		return nil, fmt.Errorf("no matching devlink port found with MAC address %s", mac.String())
	}

	if len(foundPorts) > 1 {
		return nil, fmt.Errorf("multiple matching(%d) devlink ports found with MAC address %s", len(foundPorts), mac.String())
	}

	port := foundPorts[0]
	if port.DeviceName == "" || port.ControllerNumber == nil || port.PfNumber == nil {
		return nil, fmt.Errorf("unexpected result from netlink. devlink port with MAC address %s has missing attributes", mac.String())
	}

	return &RepresentorPortParams{
		ECPF:             port.DeviceName,
		ControllerNumber: *port.ControllerNumber,
		PFNumber:         *port.PfNumber,
	}, nil
}
