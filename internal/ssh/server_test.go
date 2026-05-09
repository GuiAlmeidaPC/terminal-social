package ssh

import (
	"net"
	"testing"
)

type stringAddr string

func (a stringAddr) Network() string { return "tcp" }
func (a stringAddr) String() string  { return string(a) }

func TestRemoteIPStripsPort(t *testing.T) {
	cases := []struct {
		name string
		addr net.Addr
		want string
	}{
		{name: "ipv4", addr: stringAddr("192.0.2.10:51234"), want: "192.0.2.10"},
		{name: "ipv6", addr: stringAddr("[2001:db8::1]:51234"), want: "2001:db8::1"},
		{name: "unknown", addr: stringAddr("not-a-hostport"), want: "not-a-hostport"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := remoteIP(tc.addr); got != tc.want {
				t.Fatalf("remoteIP() = %q, want %q", got, tc.want)
			}
		})
	}
}
