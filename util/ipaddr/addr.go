package ipaddr

import (
	"fmt"
	"net"

	"github.com/vishvananda/netlink"
)

var (
	DefaultMask net.IPMask = net.CIDRMask(32, 32)
)

// An injectable interface for running ip addr commands.
type Interface interface {
	// AddAddr binds an IP addr to one network interface.
	AddAddr(ip net.IP) error

	// DeleteAddr removes an IP addr binding.
	DeleteAddr(ip net.IP) error

	// GetAddrs returns IP address map
	GetAddrs() (map[string]bool, error)
}

// runner implements Interface in terms of exec("ip addr")
type runner struct {
	link netlink.Link
}

func New(device string) (Interface, error) {
	link, err := netlink.LinkByName(device)
	if err != nil {
		return nil, fmt.Errorf("failed to find netlink named %s", device)
	}
	if link.Attrs().Flags&net.FlagUp == 0 {
		return nil, fmt.Errorf("netlink %s is DOWN, please make sure the netlink is up")
	}
	return &runner{
		link: link,
	}, nil
}

func (runner *runner) AddAddr(ip net.IP) error {
	addr := &netlink.Addr{
		IPNet: &net.IPNet{
			IP:   ip,
			Mask: DefaultMask,
		},
	}
	if err := netlink.AddrAdd(runner.link, addr); err != nil {
		return err
	}
	return nil
}

func (runner *runner) DeleteAddr(ip net.IP) error {
	addr := &netlink.Addr{
		IPNet: &net.IPNet{
			IP:   ip,
			Mask: DefaultMask,
		},
	}
	if err := netlink.AddrDel(runner.link, addr); err != nil {
		return err
	}
	return nil
}

func (runner *runner) GetAddrs() (map[string]bool, error) {
	addrList, err := netlink.AddrList(runner.link, netlink.FAMILY_V4)
	if err != nil {
		return nil, fmt.Errorf("failed to list address of netlink %s: %v", runner.link.Attrs().Name, err)
	}
	ips := make(map[string]bool)
	for i := range addrList {
		addr := addrList[i]
		ip := addr.IPNet.IP
		ips[ip.String()] = true
	}
	return ips, nil
}
