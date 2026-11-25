package routing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/PapilioVPN/node/internal/settings"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const protocol = 242

type Service struct {
	options *settings.WireGuardRouting
	route   *netlink.Handle
	nft     string
}

func (service *Service) Enabled() bool { return service.options != nil }

func (service *Service) open(context.Context) error {
	nft, err := exec.LookPath("nft")
	if err != nil {
		return fmt.Errorf("wireguard routing requires nft: %w", err)
	}
	route, err := netlink.NewHandle(unix.NETLINK_ROUTE)
	if err != nil {
		return err
	}
	if err := route.SetSocketTimeout(250 * time.Millisecond); err != nil {
		route.Close()
		return err
	}
	service.nft, service.route = nft, route
	return nil
}

func (service *Service) close(context.Context) error {
	service.route.Close()
	return nil
}

func tableName(alias string) string {
	sum := sha256.Sum256([]byte(alias))
	return "node_wg_" + hex.EncodeToString(sum[:8])
}

func (service *Service) Apply(ctx context.Context, name, alias string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	egress, err := service.egressLink(name)
	if err != nil {
		return fmt.Errorf("wireguard egress link: %w", err)
	}
	table := tableName(alias)
	owned, err := service.tableExists(ctx, table)
	if err != nil {
		return err
	}
	options := service.options
	var routePresent, rulePresent bool
	if options.Table != 0 {
		routePresent, rulePresent, err = service.checkPolicy(name, egress.Attrs().Index, owned)
		if err != nil {
			return err
		}
	}
	if err := ensureForwarding(options.EnableIPv4Forwarding); err != nil {
		return err
	}
	egressName := egress.Attrs().Name
	if !owned {
		var rules strings.Builder
		fmt.Fprintf(&rules, "add table ip %s\n", table)
		fmt.Fprintf(&rules, "add chain ip %s forward { type filter hook forward priority filter; policy accept; }\n", table)
		fmt.Fprintf(&rules, "add rule ip %s forward iifname %q oifname %q accept\n", table, name, egressName)
		fmt.Fprintf(&rules, "add rule ip %s forward iifname %q oifname %q ct state established,related accept\n", table, egressName, name)
		if options.Masquerade {
			fmt.Fprintf(&rules, "add chain ip %s postrouting { type nat hook postrouting priority srcnat; policy accept; }\n", table)
			fmt.Fprintf(&rules, "add rule ip %s postrouting iifname %q oifname %q masquerade\n", table, name, egressName)
		}
		cmd := exec.CommandContext(ctx, service.nft, "-f", "-")
		cmd.Stdin = strings.NewReader(rules.String())
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("install wireguard nftables rules: %w: %s", err, out)
		}
	}
	if options.Table != 0 && !routePresent {
		route := service.defaultRoute(egress.Attrs().Index)
		if err := service.route.RouteAdd(&route); err != nil {
			return err
		}
	}
	if options.Table != 0 && !rulePresent {
		if err := service.route.RuleAdd(service.rule(name)); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func ensureForwarding(enable bool) error {
	const path = "/proc/sys/net/ipv4/ip_forward"
	value, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(value)) == "1" {
		return nil
	}
	if !enable {
		return errors.New("wireguard routing requires IPv4 forwarding in the network namespace")
	}
	return os.WriteFile(path, []byte("1\n"), 0)
}

func (service *Service) egressLink(ingress string) (netlink.Link, error) {
	if service.options.Egress != "" {
		return service.route.LinkByName(service.options.Egress)
	}
	routes, err := service.route.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{Table: unix.RT_TABLE_MAIN}, netlink.RT_FILTER_TABLE)
	if err != nil {
		return nil, err
	}
	var best netlink.Route
	found, ambiguous := false, false
	for _, route := range routes {
		if !isDefault(route) || route.Type != unix.RTN_UNICAST {
			continue
		}
		if !found || route.Priority < best.Priority {
			best, found, ambiguous = route, true, len(route.MultiPath) != 0
		} else if route.Priority == best.Priority && (route.LinkIndex != best.LinkIndex || len(route.MultiPath) != 0) {
			ambiguous = true
		}
	}
	if !found || ambiguous || best.LinkIndex == 0 {
		return nil, errors.New("wireguard requires an explicit egress when the main IPv4 default route is unavailable or ambiguous")
	}
	link, err := service.route.LinkByIndex(best.LinkIndex)
	if err == nil && link.Attrs().Name == ingress {
		return nil, errors.New("wireguard default egress is the managed ingress interface")
	}
	return link, err
}

func (service *Service) Remove(ctx context.Context, name, alias string) error {
	table := tableName(alias)
	owned, err := service.tableExists(ctx, table)
	if err != nil || !owned {
		return err
	}
	if service.options.Table != 0 {
		rule := service.rule(name)
		rules, err := service.route.RuleList(netlink.FAMILY_V4)
		if err != nil {
			return err
		}
		for _, existing := range rules {
			if exactRule(existing, *rule) {
				if err := service.route.RuleDel(rule); err != nil && !errors.Is(err, unix.ENOENT) {
					return err
				}
			}
		}
		routes, err := service.policyRoutes()
		if err != nil {
			return err
		}
		for _, route := range routes {
			if route.Protocol == protocol && isDefault(route) && route.Gw.Equal(net.ParseIP(service.options.Gateway)) {
				if err := service.route.RouteDel(&route); err != nil && !errors.Is(err, unix.ESRCH) {
					return err
				}
			}
		}
	}
	if out, err := exec.CommandContext(ctx, service.nft, "delete", "table", "ip", table).CombinedOutput(); err != nil {
		return fmt.Errorf("remove wireguard nftables rules: %w: %s", err, out)
	}
	return nil
}

func (service *Service) tableExists(ctx context.Context, name string) (bool, error) {
	output, err := exec.CommandContext(ctx, service.nft, "-j", "list", "tables").Output()
	if err != nil {
		return false, fmt.Errorf("list nftables ownership: %w", err)
	}
	var response struct {
		NFTables []struct {
			Table *struct {
				Family string
				Name   string
			}
		}
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return false, err
	}
	for _, entry := range response.NFTables {
		if entry.Table != nil && entry.Table.Family == "ip" && entry.Table.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func (service *Service) rule(name string) *netlink.Rule {
	rule := netlink.NewRule()
	rule.Family = netlink.FAMILY_V4
	rule.Table = service.options.Table
	rule.Priority = service.options.Priority
	rule.IifName = name
	rule.Protocol = protocol
	return rule
}

func (service *Service) defaultRoute(egress int) netlink.Route {
	return netlink.Route{LinkIndex: egress, Family: netlink.FAMILY_V4, Table: service.options.Table, Protocol: protocol,
		Dst: &net.IPNet{IP: net.IPv4zero, Mask: net.CIDRMask(0, 32)}, Gw: net.ParseIP(service.options.Gateway)}
}

func (service *Service) policyRoutes() ([]netlink.Route, error) {
	return service.route.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{Table: service.options.Table}, netlink.RT_FILTER_TABLE)
}

func isDefault(route netlink.Route) bool {
	return route.Dst == nil || route.Dst.IP.IsUnspecified() && route.Dst.String() == "0.0.0.0/0"
}

func exactRule(actual, expected netlink.Rule) bool {
	return actual.Priority == expected.Priority && actual.Table == expected.Table && actual.IifName == expected.IifName && actual.Protocol == expected.Protocol &&
		actual.Src == nil && actual.Dst == nil && actual.Mark == 0 && actual.OifName == ""
}

func (service *Service) checkPolicy(name string, egress int, owned bool) (routePresent, rulePresent bool, err error) {
	routes, err := service.policyRoutes()
	if err != nil {
		return false, false, err
	}
	want := service.defaultRoute(egress)
	for _, route := range routes {
		if !owned || routePresent || !isDefault(route) || route.Protocol != protocol || route.LinkIndex != egress || !route.Gw.Equal(want.Gw) {
			return false, false, errors.New("wireguard policy table already in use")
		}
		routePresent = true
	}
	rules, err := service.route.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return false, false, err
	}
	rule := service.rule(name)
	for _, existing := range rules {
		if existing.Priority != rule.Priority && existing.Table != rule.Table {
			continue
		}
		if !owned || rulePresent || !exactRule(existing, *rule) {
			return false, false, errors.New("wireguard policy rule already in use")
		}
		rulePresent = true
	}
	return routePresent, rulePresent, nil
}
