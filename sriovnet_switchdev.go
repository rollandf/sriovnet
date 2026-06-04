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
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/vishvananda/netlink"

	utilfs "github.com/k8snetworkplumbingwg/sriovnet/pkg/utils/filesystem"
	"github.com/k8snetworkplumbingwg/sriovnet/pkg/utils/netlinkops"
)

const (
	netdevPhysSwitchID = "phys_switch_id"
	netdevPhysPortName = "phys_port_name"
)

type PortFlavour uint16

// Keep things consistent with netlink lib constants
// nolint:revive,stylecheck
const (
	PORT_FLAVOUR_PHYSICAL = iota
	PORT_FLAVOUR_CPU
	PORT_FLAVOUR_DSA
	PORT_FLAVOUR_PCI_PF
	PORT_FLAVOUR_PCI_VF
	PORT_FLAVOUR_VIRTUAL
	PORT_FLAVOUR_UNUSED
	PORT_FLAVOUR_PCI_SF
	PORT_FLAVOUR_UNKNOWN = 0xffff
)

var (
	// ErrRepresentorNotFound is returned when a representor is not found.
	ErrRepresentorNotFound = errors.New("representor not found")
)

var (
	// Regex that matches on the physical/uplink port name
	physPortRepRegex = regexp.MustCompile(`^p(\d+)$`)
	// Regex that matches on PF representor port name. These ports exists on DPUs and represents ports on Host.
	pfPortRepRegex = regexp.MustCompile(`^(?:c\d+)?pf(\d+)$`)
	// Regex that matches on VF representor port name for a local VF.
	vfPortRepRegex = regexp.MustCompile(`^pf(\d+)vf(\d+)$`)
	// Regex that matches on VF representor port name with controller index. These ports exists on DPUs. and represent VFs on Host.
	vfPortRepRegexWithControllerIndex = regexp.MustCompile(`^c\d+pf(\d+)vf(\d+)$`)
	// Regex that matches on SF representor port name
	sfPortRepRegex = regexp.MustCompile(`^pf(\d+)sf(\d+)$`)
	// Regex that matches on SF representor port name with controller index. These ports exists on DPUs. and represent SFs on Host.
	sfPortRepRegexWithControllerIndex = regexp.MustCompile(`^c\d+pf(\d+)sf(\d+)$`)
)

func parseIndexFromPhysPortName(portName string, regex *regexp.Regexp) (pfRepIndex, vfRepIndex int, err error) {
	matches := regex.FindStringSubmatch(portName)
	//nolint:gomnd
	if len(matches) != 3 {
		err = fmt.Errorf("failed to parse portName %s", portName)
	} else {
		pfRepIndex, err = strconv.Atoi(matches[1])
		if err == nil {
			vfRepIndex, err = strconv.Atoi(matches[2])
		}
	}
	return pfRepIndex, vfRepIndex, err
}

func parseVFPortName(physPortName string) (pfRepIndex, vfRepIndex int, err error) {
	for _, regex := range []*regexp.Regexp{vfPortRepRegex, vfPortRepRegexWithControllerIndex} {
		if regex.MatchString(physPortName) {
			return parseIndexFromPhysPortName(physPortName, regex)
		}
	}

	return pfRepIndex, vfRepIndex, fmt.Errorf("failed to parse vf port name %s", physPortName)
}

func isSwitchdev(netdevice string) bool {
	swIDFile := filepath.Join(NetSysDir, netdevice, netdevPhysSwitchID)
	physSwitchID, err := utilfs.Fs.ReadFile(swIDFile)
	if err != nil {
		return false
	}
	if len(physSwitchID) != 0 {
		return true
	}
	return false
}

// getUplinkRepresentorDevlink returns the uplink representor netdev name for a given PCI address
func getUplinkRepresentorDevlink(pciAddress string) (string, error) {
	// Note(adrianc): we do not check that the devlink device eswitch mode is in switchdev mode,
	// the implementation should work for both switchdev and legacy modes.

	// list all ports. physical ports are not registered under the devlink device with the given PCI address.
	// e.g:
	//	auxiliary/mlx5_core.eth.5/262143: type eth netdev p1 flavour physical port 1 splittable false
	ports, err := netlinkops.GetNetlinkOps().DevLinkGetAllPortList()
	if err != nil {
		return "", fmt.Errorf("failed to list devlink ports: %w", err)
	}

	// filter ports with flavor physical with non empty netdevice name
	// Note(adrianc): a devlink port may not have a netdevice if the device is in a different namespace
	// or if the port does not have a netdevice yet.
	var candidateNetdevs []string
	for _, port := range ports {
		if port.PortFlavour == uint16(PORT_FLAVOUR_PHYSICAL) && port.NetdeviceName != "" {
			candidateNetdevs = append(candidateNetdevs, port.NetdeviceName)
		}
	}

	// assuming at most only one devlink port of type physical exists for a given PCI address
	// find the netdev that is registered under the given PCI address
	for _, netdev := range candidateNetdevs {
		expectedNetdevPath := filepath.Join(PciSysDir, pciAddress, "net", netdev)
		if _, err := utilfs.Fs.Stat(expectedNetdevPath); err == nil {
			return netdev, nil
		}
	}

	return "", fmt.Errorf("failed to get uplink representor for %s: %w", pciAddress, ErrRepresentorNotFound)
}

// GetUplinkRepresentor gets a VF or PF PCI address (e.g '0000:03:00.4') and
// returns the uplink represntor netdev name for that VF or PF.
func GetUplinkRepresentor(pciAddress string) (string, error) {
	// get the PF PCI address, it may be the provided pciAddress or its parent pointed by physfn in case of VF.
	pfPCIAddress := pciAddress
	physfnPath := filepath.Join(PciSysDir, pciAddress, "physfn")
	if _, err := utilfs.Fs.Stat(physfnPath); err == nil {
		// physfn exists → device is a VF; read the symlink to get the PF PCI address
		lnk, err := utilfs.Fs.Readlink(physfnPath)
		if err != nil {
			return "", fmt.Errorf("failed to read link %s for %s: %w", physfnPath, pciAddress, err)
		}
		pfPCIAddress = filepath.Base(lnk)
	}

	// try to get uplink representor from devlink
	if uplink, err := getUplinkRepresentorDevlink(pfPCIAddress); err == nil {
		return uplink, nil
	}

	// fallback to sysfs
	devices, err := utilfs.Fs.ReadDir(filepath.Join(PciSysDir, pfPCIAddress, "net"))
	if err != nil {
		return "", fmt.Errorf("failed to read net dir for pf device %s: %w", pfPCIAddress, err)
	}

	for _, device := range devices {
		if isSwitchdev(device.Name()) {
			devicePhysPortName, err := getNetDevPhysPortName(device.Name())
			if err != nil {
				continue
			}

			// phys_port_name should be in formant p<port-num> e.g p0,p1,p2 ...etc.
			if !physPortRepRegex.MatchString(devicePhysPortName) {
				continue
			}

			return device.Name(), nil
		}
	}
	return "", fmt.Errorf("failed to get uplink representor for %s: %w", pciAddress, ErrRepresentorNotFound)
}

// getRepresentorDevlink returns the representor netdev name for a given device, portflavor, controller number, index and  pf number(optional).
func getRepresentorDevlink(deviceName string, flavor PortFlavour, controllerNumber uint32, index uint32, pfNum *uint16) (string, error) {
	// check for supported flavor
	if flavor != PORT_FLAVOUR_PCI_VF && flavor != PORT_FLAVOUR_PCI_SF && flavor != PORT_FLAVOUR_PCI_PF {
		return "", fmt.Errorf("unsupported flavor %d", flavor)
	}

	ports, err := netlinkops.GetNetlinkOps().DevLinkGetDevicePortList("pci", deviceName)
	if err != nil {
		return "", fmt.Errorf("failed to get devlink ports for pci device %s: %w", deviceName, err)
	}

	for _, port := range ports {
		// skip ports that are not of the given flavor
		if port.PortFlavour != uint16(flavor) {
			continue
		}

		if port.ControllerNumber == nil || *port.ControllerNumber != controllerNumber {
			continue
		}

		// if pfNum is specified, check that the port pf number matches.
		if pfNum != nil && (port.PfNumber == nil || *port.PfNumber != *pfNum) {
			continue
		}

		// get devlink port pf/sf/vf number
		var dpIndex uint32
		switch flavor {
		case PORT_FLAVOUR_PCI_PF:
			if port.PfNumber == nil {
				return "", fmt.Errorf("unexpected result from netlink. devlink port of type pf has no pf number. pci/%s/%d", deviceName, port.PortIndex)
			}
			dpIndex = uint32(*port.PfNumber)
		case PORT_FLAVOUR_PCI_VF:
			if port.VfNumber == nil {
				return "", fmt.Errorf("unexpected result from netlink. devlink port of type vf has no vf number. pci/%s/%d", deviceName, port.PortIndex)
			}
			dpIndex = uint32(*port.VfNumber)
		case PORT_FLAVOUR_PCI_SF:
			if port.SfNumber == nil {
				return "", fmt.Errorf("unexpected result from netlink. devlink port of type sf has no sf number. pci/%s/%d", deviceName, port.PortIndex)
			}
			dpIndex = *port.SfNumber
		}

		if dpIndex != index {
			continue
		}

		// we found the matching devlink port, the netdevice attribute is the representor netdev name
		if port.NetdeviceName == "" {
			return "", fmt.Errorf("unexpected result from netlink. devlink port of type %d has no netdevice name. pci/%s/%d", flavor, deviceName, port.PortIndex)
		}

		return port.NetdeviceName, nil
	}

	return "", fmt.Errorf("failed to get representor via devlink: device %s, flavor %d, controller %d, index %d: %w",
		deviceName, flavor, controllerNumber, index, ErrRepresentorNotFound)
}

// GetVfRepresentor returns the VF representor netdev name for a given uplink netdev and vfIndex.
func GetVfRepresentor(uplink string, vfIndex int) (string, error) {
	// if uplink is not switchdev, return error early
	if !isSwitchdev(uplink) {
		return "", fmt.Errorf("uplink %s is not a switchdev", uplink)
	}

	if vfIndex < 0 {
		return "", fmt.Errorf("vfIndex %d is negative", vfIndex)
	}

	// get uplink pci device
	uplinkPCI, err := getPCIFromDeviceName(uplink)
	if err != nil {
		return "", fmt.Errorf("failed to get pci address for uplink %s: %v", uplink, err)
	}

	// try to get representor from devlink
	representor, err := getRepresentorDevlink(uplinkPCI, PORT_FLAVOUR_PCI_VF, 0, uint32(vfIndex), nil)
	if err == nil {
		return representor, nil
	}

	// try to get representor from phys_port_name

	// representors of a specific uplinkare expected to be linked with the same device as the uplink
	pfLinkPath := filepath.Join(NetSysDir, uplink, "device", "net")
	devices, err := utilfs.Fs.ReadDir(pfLinkPath)
	if err != nil {
		return "", err
	}
	for _, device := range devices {
		physPortNameStr, err := getNetDevPhysPortName(device.Name())
		if err != nil {
			continue
		}

		_, vfRepIndex, err := parseIndexFromPhysPortName(physPortNameStr, vfPortRepRegex)
		if err != nil {
			continue
		}

		// check vfRepIndex matches the vfIndex
		if vfRepIndex == vfIndex {
			return device.Name(), nil
		}
	}
	return "", fmt.Errorf("failed to get VF representor for uplink %s, vfIndex %d: %w", uplink, vfIndex, ErrRepresentorNotFound)
}

// GetSfRepresentor returns the SF representor netdev name for a given uplink netdev and sfIndex.
func GetSfRepresentor(uplink string, sfNum int) (string, error) {
	// if uplink is not switchdev, return error early
	if !isSwitchdev(uplink) {
		return "", fmt.Errorf("uplink %s is not a switchdev", uplink)
	}

	// get uplink pci device
	uplinkPCI, err := getPCIFromDeviceName(uplink)
	if err != nil {
		return "", fmt.Errorf("failed to get pci address for uplink %s: %v", uplink, err)
	}

	// try to get representor from devlink
	representor, err := getRepresentorDevlink(uplinkPCI, PORT_FLAVOUR_PCI_SF, 0, uint32(sfNum), nil)
	if err == nil {
		return representor, nil
	}

	// try to get representor from phys_port_name
	pfNetPath := filepath.Join(NetSysDir, uplink, "device", "net")
	devices, err := utilfs.Fs.ReadDir(pfNetPath)
	if err != nil {
		return "", err
	}

	for _, device := range devices {
		physPortNameStr, err := getNetDevPhysPortName(device.Name())
		if err != nil {
			continue
		}
		_, sfRepIndex, err := parseIndexFromPhysPortName(physPortNameStr, sfPortRepRegex)
		if err != nil {
			continue
		}
		if sfRepIndex == sfNum {
			return device.Name(), nil
		}
	}
	return "", fmt.Errorf("failed to get SF representor for uplink %s, sfNum %d: %w", uplink, sfNum, ErrRepresentorNotFound)
}

func getNetDevPhysPortName(netDev string) (string, error) {
	devicePortNameFile := filepath.Join(NetSysDir, netDev, netdevPhysPortName)
	physPortName, err := utilfs.Fs.ReadFile(devicePortNameFile)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(physPortName)), nil
}

// findNetdevWithPortNameCriteria returns representor netdev that matches a criteria function on the
// physical port name
func findNetdevWithPortNameCriteria(netdir string, criteria func(string) bool) (string, error) {
	netdevs, err := utilfs.Fs.ReadDir(netdir)
	if err != nil {
		return "", err
	}

	for _, netdev := range netdevs {
		// find matching VF representor
		netdevName := netdev.Name()

		// skip non switchdev netdevs
		if !isSwitchdev(netdevName) {
			continue
		}

		portName, err := getNetDevPhysPortName(netdevName)
		if err != nil {
			continue
		}

		if criteria(portName) {
			return netdevName, nil
		}
	}
	return "", fmt.Errorf("no representor matched criteria")
}

// getPortIndexDevlink returns the port index of a representor from its devlink port.
// It supports VF and SF representor port flavors.
func getPortIndexDevlink(repNetDev string) (int, error) {
	port, err := netlinkops.GetNetlinkOps().DevLinkGetPortByNetdevName(repNetDev)
	if err != nil {
		return 0, err
	}

	switch port.PortFlavour {
	case PORT_FLAVOUR_PCI_VF:
		if port.VfNumber == nil {
			return 0, fmt.Errorf("unexpected result from netlink. devlink port of type vf has no vf number")
		}
		return int(*port.VfNumber), nil
	case PORT_FLAVOUR_PCI_SF:
		if port.SfNumber == nil {
			return 0, fmt.Errorf("unexpected result from netlink. devlink port of type sf has no sf number")
		}
		return int(*port.SfNumber), nil
	default:
		return 0, fmt.Errorf("unsupported port flavour %d for netdev %s", port.PortFlavour, repNetDev)
	}
}

// GetPortIndexFromRepresentor finds the index of a representor from its network device name.
// Supports VF and SF. For multiple port flavors, the same ID could be returned, i.e.
//
//	pf0vf10 and pf0sf10
//
// will return the same port ID. To further differentiate the ports, use GetRepresentorPortFlavour
func GetPortIndexFromRepresentor(repNetDev string) (int, error) {
	flavor, err := GetRepresentorPortFlavour(repNetDev)
	if err != nil {
		return 0, err
	}

	if flavor != PORT_FLAVOUR_PCI_VF && flavor != PORT_FLAVOUR_PCI_SF {
		return 0, fmt.Errorf("unsupported port flavor for netdev %s", repNetDev)
	}

	// try to get port index from devlink
	if portIndex, err := getPortIndexDevlink(repNetDev); err == nil {
		return portIndex, nil
	}

	// fallback to sysfs
	physPortName, err := getNetDevPhysPortName(repNetDev)
	if err != nil {
		return 0, fmt.Errorf("failed to get device %s physical port name: %v", repNetDev, err)
	}

	typeToRegex := map[PortFlavour][]*regexp.Regexp{
		PORT_FLAVOUR_PCI_VF: {vfPortRepRegex, vfPortRepRegexWithControllerIndex},
		PORT_FLAVOUR_PCI_SF: {sfPortRepRegex, sfPortRepRegexWithControllerIndex},
	}

	for _, regex := range typeToRegex[flavor] {
		if regex.MatchString(physPortName) {
			_, repIndex, err := parseIndexFromPhysPortName(physPortName, regex)
			if err != nil {
				return 0, fmt.Errorf("failed to parse the physical port name of device %s: %v", repNetDev, err)
			}

			return repIndex, nil
		}
	}

	return 0, fmt.Errorf("failed to get port index for representor %s. no matching regex found for phys_port_name %s",
		repNetDev, physPortName)
}

// GetVfRepresentorDPU returns VF representor on DPU for a host VF identified by pfID and vfIndex
//
// Deprecated: use GetVfRepresentorFromPortParams instead.
func GetVfRepresentorDPU(pfID, vfIndex string) (string, error) {
	// TODO(Adrianc): This method should change to get switchID and vfIndex as input, then common logic can
	// be shared with GetVfRepresentor, backward compatibility should be preserved when this happens.

	// pfID should be 0 or 1
	if pfID != "0" && pfID != "1" {
		return "", fmt.Errorf("unexpected pfID(%s). It should be 0 or 1", pfID)
	}

	// vfIndex should be an unsinged integer provided as a decimal number
	if _, err := strconv.ParseUint(vfIndex, 10, 32); err != nil {
		return "", fmt.Errorf("unexpected vfIndex(%s). It should be an unsigned decimal number", vfIndex)
	}

	// match port name with external controller index
	// NOTE: no support for Multi-Chassis DPUs
	expectedPhysPortName := fmt.Sprintf("c1pf%svf%s", pfID, vfIndex)
	netdev, err := findNetdevWithPortNameCriteria(NetSysDir, func(portName string) bool {
		return portName == expectedPhysPortName
	})

	if err == nil {
		return netdev, nil
	}

	// match port name without controller index (legacy)
	// NOTE: here we assume the only VF representors on the DPU are for host VFs (and not for local VFs).
	expectedPhysPortName = fmt.Sprintf("pf%svf%s", pfID, vfIndex)
	netdev, err = findNetdevWithPortNameCriteria(NetSysDir, func(portName string) bool {
		return portName == expectedPhysPortName
	})

	if err == nil {
		return netdev, nil
	}

	return "", fmt.Errorf("failed to get VF representor for pfID: %s, vfIndex: %s: %w", pfID, vfIndex, ErrRepresentorNotFound)
}

// GetSfRepresentorDPU returns SF representor on DPU for a host SF identified by pfID and sfIndex
//
// Deprecated: use GetSfRepresentorFromPortParams instead.
func GetSfRepresentorDPU(pfID, sfIndex string) (string, error) {
	// pfID should be 0 or 1
	if pfID != "0" && pfID != "1" {
		return "", fmt.Errorf("unexpected pfID(%s). It should be 0 or 1", pfID)
	}

	// sfIndex should be an unsinged integer provided as a decimal number
	if _, err := strconv.ParseUint(sfIndex, 10, 32); err != nil {
		return "", fmt.Errorf("unexpected sfIndex(%s). It should be an unsigned decimal number", sfIndex)
	}

	// match port name with external controller index
	// NOTE: no support for Multi-Chassis DPUs
	expectedPhysPortName := fmt.Sprintf("c1pf%ssf%s", pfID, sfIndex)
	netdev, err := findNetdevWithPortNameCriteria(NetSysDir, func(portName string) bool {
		return portName == expectedPhysPortName
	})

	if err == nil {
		return netdev, nil
	}

	return "", fmt.Errorf("failed to get SF representor for pfID: %s, sfIndex: %s: %w", pfID, sfIndex, ErrRepresentorNotFound)
}

// GetPfRepresentorDPU returns PF representor on DPU for a host PF identified by its ID.
//
// Deprecated: use GetPfRepresentorFromPortParams instead.
func GetPfRepresentorDPU(pfID string) (string, error) {
	// pfID should be 0 or 1
	if pfID != "0" && pfID != "1" {
		return "", fmt.Errorf("unexpected pfID(%s). It should be 0 or 1", pfID)
	}

	// match port name with external controller index
	// NOTE: no support for Multi-Chassis DPUs
	expectedPhysPortName := fmt.Sprintf("c1pf%s", pfID)
	netdev, err := findNetdevWithPortNameCriteria(NetSysDir, func(portName string) bool {
		return portName == expectedPhysPortName
	})

	if err == nil {
		return netdev, nil
	}

	// match port name without controller index (legacy)
	expectedPhysPortName = fmt.Sprintf("pf%s", pfID)
	netdev, err = findNetdevWithPortNameCriteria(NetSysDir, func(portName string) bool {
		return portName == expectedPhysPortName
	})

	if err == nil {
		return netdev, nil
	}

	return "", fmt.Errorf("failed to get PF representor for pfID: %s: %w", pfID, ErrRepresentorNotFound)
}

// GetRepresentorPortFlavour returns the representor port flavour
// Note: this method does not support old representor names used by old kernels
// e.g <vf_num> and will return PORT_FLAVOUR_UNKNOWN for such cases.
func GetRepresentorPortFlavour(netdev string) (PortFlavour, error) {
	if !isSwitchdev(netdev) {
		return PORT_FLAVOUR_UNKNOWN, fmt.Errorf("net device %s is does not represent an eswitch port", netdev)
	}

	// Attempt to get information via devlink (Kernel >= 5.9.0)
	port, err := netlinkops.GetNetlinkOps().DevLinkGetPortByNetdevName(netdev)
	if err == nil {
		return PortFlavour(port.PortFlavour), nil
	}

	// Fallback to Get PortFlavour by phys_port_name
	// read phy_port_name
	portName, err := getNetDevPhysPortName(netdev)
	if err != nil {
		return PORT_FLAVOUR_UNKNOWN, err
	}

	typeToRegex := map[PortFlavour][]*regexp.Regexp{
		PORT_FLAVOUR_PHYSICAL: {physPortRepRegex},
		PORT_FLAVOUR_PCI_PF:   {pfPortRepRegex},
		PORT_FLAVOUR_PCI_VF:   {vfPortRepRegex, vfPortRepRegexWithControllerIndex},
		PORT_FLAVOUR_PCI_SF:   {sfPortRepRegex, sfPortRepRegexWithControllerIndex},
	}
	for flavour, regexs := range typeToRegex {
		for _, regex := range regexs {
			if regex.MatchString(portName) {
				return flavour, nil
			}
		}
	}
	return PORT_FLAVOUR_UNKNOWN, nil
}

// parseDPUConfigFileOutput parses the config file content of a DPU
// representor port. The format of the file is a set of <key>:<value> pairs as follows:
//
// ```
//
//	MAC        : 0c:42:a1:c6:cf:7c
//	MaxTxRate  : 0
//	State      : Follow
//
// ```
func parseDPUConfigFileOutput(out string) map[string]string {
	configMap := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		entry := strings.SplitN(line, ":", 2)
		if len(entry) != 2 {
			// unexpected line format
			continue
		}
		configMap[strings.Trim(entry[0], " \t\n")] = strings.Trim(entry[1], " \t\n")
	}
	return configMap
}

// GetRepresentorPeerMacAddress returns the MAC address of the peer netdev associated with the given
// representor netdev
// Note:
//
//	This method functionality is currently supported only on DPUs.
//	Currently only netdev representors with PORT_FLAVOUR_PCI_PF are supported
func GetRepresentorPeerMacAddress(netdev string) (net.HardwareAddr, error) {
	flavor, err := GetRepresentorPortFlavour(netdev)
	if err != nil {
		return nil, fmt.Errorf("unknown port flavour for netdev %s. %v", netdev, err)
	}
	if flavor == PORT_FLAVOUR_UNKNOWN {
		return nil, fmt.Errorf("unknown port flavour for netdev %s", netdev)
	}
	if flavor != PORT_FLAVOUR_PCI_PF {
		return nil, fmt.Errorf("unsupported port flavour for netdev %s", netdev)
	}

	// Attempt to get information via devlink (Kernel >= 5.9.0)
	port, err := netlinkops.GetNetlinkOps().DevLinkGetPortByNetdevName(netdev)
	if err == nil {
		if port.Fn != nil {
			return port.Fn.HwAddr, nil
		}
	}

	// Get information via sysfs
	// read phy_port_name
	portName, err := getNetDevPhysPortName(netdev)
	if err != nil {
		return nil, err
	}
	// Extract port num
	portNum := pfPortRepRegex.FindStringSubmatch(portName)
	if len(portNum) < 2 {
		return nil, fmt.Errorf("failed to extract physical port number from port name %s of netdev %s",
			portName, netdev)
	}
	uplinkPhysPortName := "p" + portNum[1]
	// Find uplink netdev for that port, using the parent device net dir
	netdir := filepath.Join(NetSysDir, netdev, "device", "net")
	uplinkNetdev, err := findNetdevWithPortNameCriteria(netdir, func(pname string) bool { return pname == uplinkPhysPortName })
	if err != nil {
		return nil, fmt.Errorf("failed to find uplink port for netdev %s. %v", netdev, err)
	}
	// get MAC address for netdev
	configPath := filepath.Join(NetSysDir, uplinkNetdev, "smart_nic", "pf", "config")
	out, err := utilfs.Fs.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read DPU config via uplink %s for %s. %v",
			uplinkNetdev, netdev, err)
	}
	config := parseDPUConfigFileOutput(string(out))
	macStr, ok := config["MAC"]
	if !ok {
		return nil, fmt.Errorf("MAC address not found for %s", netdev)
	}
	mac, err := net.ParseMAC(macStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse MAC address \"%s\" for %s. %v", macStr, netdev, err)
	}
	return mac, nil
}

// SetRepresentorPeerMacAddress sets the given MAC addresss of the peer netdev associated with the given
// representor netdev.
// Note: This method functionality is currently supported only for DPUs.
// Currently only netdev representors with PORT_FLAVOUR_PCI_VF are supported
func SetRepresentorPeerMacAddress(netdev string, mac net.HardwareAddr) error {
	flavor, err := GetRepresentorPortFlavour(netdev)
	if err != nil {
		return fmt.Errorf("unknown port flavour for netdev %s. %v", netdev, err)
	}
	if flavor == PORT_FLAVOUR_UNKNOWN {
		return fmt.Errorf("unknown port flavour for netdev %s", netdev)
	}
	if flavor != PORT_FLAVOUR_PCI_VF {
		return fmt.Errorf("unsupported port flavour for netdev %s", netdev)
	}

	// attempt to set MAC address via devlink
	port, err := netlinkops.GetNetlinkOps().DevLinkGetPortByNetdevName(netdev)
	if err == nil {
		// devlink port found, attempt to set MAC address via devlink
		fnAttrs := netlink.DevlinkPortFnSetAttrs{
			FnAttrs: netlink.DevlinkPortFn{
				HwAddr: mac,
			},
			HwAddrValid: true,
		}
		if err := netlinkops.GetNetlinkOps().DevLinkPortFnSet(port.BusName, port.DeviceName, port.PortIndex, fnAttrs); err == nil {
			return nil
		}
	}

	// fallback to set MAC address via sysfs
	physPortNameStr, err := getNetDevPhysPortName(netdev)
	if err != nil {
		return fmt.Errorf("failed to get phys_port_name for netdev %s: %v", netdev, err)
	}
	pfID, vfIndex, err := parseVFPortName(physPortNameStr)
	if err != nil {
		return fmt.Errorf("failed to get the pf and vf index for netdev %s "+
			"with phys_port_name %s: %v", netdev, physPortNameStr, err)
	}

	uplinkPhysPortName := fmt.Sprintf("p%d", pfID)
	// Find uplink netdev for that port, using the parent device net dir
	netdir := filepath.Join(NetSysDir, netdev, "device", "net")
	uplinkNetdev, err := findNetdevWithPortNameCriteria(netdir, func(pname string) bool { return pname == uplinkPhysPortName })
	if err != nil {
		return fmt.Errorf("failed to find netdev for physical port name %s. %v", uplinkPhysPortName, err)
	}
	vfRepName := fmt.Sprintf("vf%d", vfIndex)
	sysfsVfRepMacFile := filepath.Join(NetSysDir, uplinkNetdev, "smart_nic", vfRepName, "mac")
	_, err = utilfs.Fs.Stat(sysfsVfRepMacFile)
	if err != nil {
		return fmt.Errorf("couldn't stat VF representor's sysfs file %s: %v", sysfsVfRepMacFile, err)
	}
	err = utilfs.Fs.WriteFile(sysfsVfRepMacFile, []byte(mac.String()), 0)
	if err != nil {
		return fmt.Errorf("failed to write the MAC address %s to VF reprentor %s",
			mac.String(), sysfsVfRepMacFile)
	}
	return nil
}
