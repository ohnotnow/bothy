package bothy

import (
	"net"
	"strings"
)

// nic is the part of a network interface lanAddress looks at.
type nic struct {
	name  string
	flags net.Flags
	addrs []net.Addr
}

// virtualNICs are name prefixes of bridges that carry a private address but
// lead only to virtual machines or containers on this machine: multipass and
// other vmnet VMs on a Mac (bridge100), Docker's networks on Linux.
var virtualNICs = []string{"bridge", "docker", "br-"}

// lanAddress is the address another device on the local network would use
// to reach this machine: the private IPv4 address of the first interface
// that is up and isn't loopback, a point-to-point link (VPN tunnels,
// Tailscale's included) or a virtual bridge. "" when there isn't one.
//
// Not the address of the default route: with a full-tunnel VPN that is the
// VPN's own address, which a phone on the same Wi-Fi can't reach.
func lanAddress(nics []nic) string {
	for _, n := range nics {
		if n.flags&net.FlagUp == 0 || n.flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 {
			continue
		}
		if virtual(n.name) {
			continue
		}
		for _, a := range n.addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if ip := ipnet.IP.To4(); ip != nil && ip.IsPrivate() {
				return ip.String()
			}
		}
	}
	return ""
}

func virtual(name string) bool {
	for _, p := range virtualNICs {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// findLanAddress asks the operating system for its interfaces and picks the
// local network address from them; "" if it can't tell.
func findLanAddress() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var nics []nic
	for _, i := range ifaces {
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		nics = append(nics, nic{i.Name, i.Flags, addrs})
	}
	return lanAddress(nics)
}
