package ipaddr

import (
	"testing"
)

func TestGetAddrs(t *testing.T) {
	ipaddr, err := New("lo")
	if err != nil {
		t.Fatalf("get error when construct ip interface: %v", err)
	}
	ips, err := ipaddr.GetAddrs()
	if err != nil {
		t.Fatalf("failed to execute GetAddrs: %v", err)
	}
	if len(ips) == 0 {
		t.Fatalf("lo should have at least one ip")
	}
	if !ips["127.0.0.1"] {
		t.Fatalf("can not find local ip in ips: %v", ips)
	}
}
