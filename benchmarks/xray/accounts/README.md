# Xray account synchronization

[Benchmark dashboard](../README.md)

The scorecard covers single VLESS account synchronization over HTTP and gRPC.
The driver verifies the exact resulting membership after each request. The
longer gRPC repeat covers the same three cases as the initial integrated run.

| Coverage | Report |
| --- | --- |
| Single-account sync after branch integration | [Integrated results](../integration/README.md#single-vless-account-synchronization) |
| Single, full, and chunked sync with membership checks | [Synchronization](sync.md) |
| Direct add/remove and count, including a direct Xray baseline | [VLESS account management](vless.md) |

Full and chunked timings document different restart and stream completion
behavior. The current single-account benchmark uses the VLESS fixture.
