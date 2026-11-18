package netlink

import (
	"fmt"
	"net"
	"testing"
	"time"

	native "github.com/vishvananda/netlink"
)

type sourceFixture struct {
	links                                 []native.Link
	addresses                             []native.Addr
	linkError, addressError, timeoutError error
	linkCalls, addressCalls, closes       int
	timeouts                              []time.Duration
	afterLinks                            func()
}

func (source *sourceFixture) LinkList() ([]native.Link, error) {
	source.linkCalls++
	if source.afterLinks != nil {
		source.afterLinks()
	}
	return source.links, source.linkError
}
func (source *sourceFixture) AddrList(link native.Link, family int) ([]native.Addr, error) {
	source.addressCalls++
	if link != nil || family != native.FAMILY_ALL {
		panic("address reads must be native bulk")
	}
	return source.addresses, source.addressError
}
func (source *sourceFixture) SetSocketTimeout(value time.Duration) error {
	source.timeouts = append(source.timeouts, value)
	return source.timeoutError
}
func (source *sourceFixture) Close() { source.closes++ }
func (fixture *sourceFixture) reader() Reader {
	return Reader{Timeout: time.Second, dial: func() (source, error) { return fixture, nil }}
}

func fixtureLinks(size int) []native.Link {
	rows := make([]native.Link, size)
	for i := range rows {
		rows[i] = &native.Dummy{LinkAttrs: native.LinkAttrs{Index: i + 1, Name: fmt.Sprintf("fixture%d", i), MTU: 1500}}
	}
	return rows
}

func fixtureAddress(t *testing.T, index int, prefix string) native.Addr {
	t.Helper()
	ip, network, err := net.ParseCIDR(prefix)
	if err != nil {
		t.Fatal(err)
	}
	network.IP = ip
	return native.Addr{LinkIndex: index, IPNet: network, ValidLft: 1<<32 - 1, PreferedLft: 1<<32 - 1}
}
