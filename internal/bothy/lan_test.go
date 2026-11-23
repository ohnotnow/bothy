package bothy

import (
	"net"
	"strings"
	"testing"
)

func ipnet(s string) net.Addr {
	ip, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	n.IP = ip
	return n
}

func TestLanAddress(t *testing.T) {
	up := net.FlagUp | net.FlagBroadcast | net.FlagMulticast
	loop := nic{"lo0", net.FlagUp | net.FlagLoopback, []net.Addr{ipnet("127.0.0.1/8")}}
	vpn := nic{"utun7", net.FlagUp | net.FlagPointToPoint, []net.Addr{ipnet("10.0.0.5/32")}}
	tailnet := nic{"utun4", net.FlagUp | net.FlagPointToPoint, []net.Addr{ipnet("100.101.102.103/32")}}
	vm := nic{"bridge100", up, []net.Addr{ipnet("192.168.64.1/24")}}
	dockerNet := nic{"docker0", up, []net.Addr{ipnet("172.17.0.1/16")}}
	wifi := nic{"en0", up, []net.Addr{ipnet("fe80::1/64"), ipnet("192.168.0.20/24")}}
	down := nic{"en1", net.FlagBroadcast, []net.Addr{ipnet("192.168.0.30/24")}}
	public := nic{"en2", up, []net.Addr{ipnet("203.0.113.9/24")}}

	for _, tc := range []struct {
		name string
		nics []nic
		want string
	}{
		{"wifi behind a vpn, a vm bridge and docker", []nic{loop, vpn, tailnet, vm, dockerNet, wifi}, "192.168.0.20"},
		{"skips an interface that is down", []nic{down, wifi}, "192.168.0.20"},
		{"skips a public address", []nic{public, wifi}, "192.168.0.20"},
		{"nothing but loopback and tunnels", []nic{loop, vpn, tailnet}, ""},
		{"no interfaces", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := lanAddress(tc.nics); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInfoShowsTheLanAddressForOpenPorts(t *testing.T) {
	t.Run("an address is found", func(t *testing.T) {
		a, _, out := infoApp(t, "")
		a.LanAddress = func() string { return "192.168.0.20" }
		a.Main([]string{"info"})
		got := out.String()
		if !strings.Contains(got, "http://192.168.0.20:54124  (open to the network)") {
			t.Errorf("open port not shown on the lan address:\n%s", got)
		}
		if !strings.Contains(got, "http://127.0.0.1:54123") || !strings.Contains(got, "127.0.0.1:4406") {
			t.Errorf("loopback-only ports should stay on 127.0.0.1:\n%s", got)
		}
	})
	t.Run("none found", func(t *testing.T) {
		a, _, out := infoApp(t, "")
		a.LanAddress = func() string { return "" }
		a.Main([]string{"info"})
		if !strings.Contains(out.String(), "http://127.0.0.1:54124  (open to the network)") {
			t.Errorf("got:\n%s", out)
		}
	})
	t.Run("not looked up when nothing is open", func(t *testing.T) {
		a, _, _ := infoApp(t, "")
		a.Output = func(args []string) ([]byte, error) { return []byte(allLoopback), nil }
		asked := false
		a.LanAddress = func() string { asked = true; return "192.168.0.20" }
		a.Main([]string{"info"})
		if asked {
			t.Error("looked up the lan address with no port open")
		}
	})
}
