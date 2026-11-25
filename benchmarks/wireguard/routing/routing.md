# WireGuard IPv4 forwarding, NAT and policy routing

Configure the following options under `wireguard` with managed mode
enabled. Enable IPv4 forwarding in the Node network namespace and install
the `nft` executable before starting Node.

```yaml
routing:
  egress_interface: eth1
  masquerade: true
  policy_table: 20101
  policy_priority: 19001
  gateway: 198.51.100.1
```

Select a dedicated policy table and a free priority. Omit the policy fields
to use existing routes. The gateway is optional for directly reachable
egress destinations. Node applies IPv4 rules scoped to the selected WireGuard
and egress interfaces. Existing firewall policies govern forwarding. An
ownership table is derived from the managed link alias. Stop removes its
policy entry, route and nftables table. Keep routing settings with the
checkpoint during recovery and stop the managed interface before editing them.

The isolated integration test sends real WireGuard traffic to an IPv4
destination reachable only through the selected policy table. The target
replies to the NAT source. Stop removes the managed rules and preserves
an unrelated table.

The paired HTTPS benchmark enables policy routing and scoped masquerade
on both implementations. The fixture checks policy rule installation and
removal for every sample. Times are milliseconds and each row reports
the median of four observations.

| Operation | Node | PasarGuard |
| --- | ---: | ---: |
| Start with routing | 11.102 | 34.182 |
| Stop with routing | 105.100 | 110.309 |

Use `--routing` with the [matched fixture](../interfaces/lifecycle.md#reproduce).
[Raw timings](results/wg-routing-https.csv)
