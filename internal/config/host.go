package config

import (
	"net"
	"strings"
)

// HostLAN is the defaults.host value that means "this machine's current LAN
// IPv4". It is re-resolved every time task specs are built, so a DHCP lease
// change (e.g. after a router restart) does not leave a stale address in
// ringmaster.yaml.
const HostLAN = "lan"

// BindHost resolves defaults.host to the concrete address interpolated into
// {host}: "" means loopback, "lan" is detected now (falling back to loopback
// when no LAN is up), and anything else is used as written.
func (d Defaults) BindHost() string {
	switch d.Host {
	case "":
		return "127.0.0.1"
	case HostLAN:
		if ip := lanIPv4(); ip != "" {
			return ip
		}
		return "127.0.0.1"
	}
	return d.Host
}

// DynamicHost reports whether BindHost can change between calls.
func (d Defaults) DynamicHost() bool { return d.Host == HostLAN }

// lanIPv4 returns the private IPv4 of the interface that carries the default
// route, or "" if there is none.
func lanIPv4() string {
	// Dialing UDP sends no packets; it only asks the OS which local address
	// it would route from. This skips virtual adapters (WSL, Hyper-V) that a
	// plain interface scan would also see.
	if c, err := net.Dial("udp4", "192.0.2.1:9"); err == nil {
		ip := c.LocalAddr().(*net.UDPAddr).IP
		c.Close()
		if ip.IsPrivate() {
			return ip.String()
		}
	}
	// No default route (router down, offline): take the first private IPv4 on
	// an up, non-virtual interface.
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || isVirtual(ifc.Name) {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && n.IP.IsPrivate() {
				return n.IP.String()
			}
		}
	}
	return ""
}

func isVirtual(name string) bool {
	name = strings.ToLower(name)
	for _, v := range []string{"vethernet", "wsl", "hyper-v", "virtualbox", "vmware", "docker", "vbox"} {
		if strings.Contains(name, v) {
			return true
		}
	}
	return false
}
