// Package traffic owns native user, inbound and outbound traffic observations
// and explicitly destructive resets. All operations share the instance-owned
// native channel and the normal management-controller transport boundary.
//
// Exact reads use one GetStats(false); pairs use two targeted calls. Collections
// use one QueryStats(false), whose native substring selection is narrowed by an
// exact scope prefix and direction suffix. Opaque names may contain >>>. Native
// signed int64 bytes, registered zero and missing counters remain distinct.
// Counters are ephemeral, can wrap/reset, and are not durable billing totals.
//
// Collections admit a 32 MiB native reply and 65536 decoded records. These limits
// do not cap Xray's scan/allocation or protobuf heap usage. At most 50 counters
// are returned per page. Continuations read a retained observation, not live
// values, without refetching Xray; their original timestamp/source/generation
// remain unchanged. They expire after one minute, on a new successful scan for
// that instance, or an observed execution replacement. Up to 32 retained scans
// share a 32 MiB counter-storage budget. A fresh scan is necessary after expiry.
// Reads admit 32 native observations globally and eight per instance, without
// a waiting queue. Local execution generation is not endpoint attestation.
//
// Resets are separate counter-resets, pair-resets and scope-resets HTTP
// subresources, additive TrafficService RPCs and versioned NATS subjects. Exact
// reset uses one native swap. Pair reset attempts uplink then downlink, allowing
// an absent direction. Scope reset first scans without effects and then swaps
// each exact selected counter sequentially: one scan plus N swaps, not native
// bulk. The caller must bound discovery to 1..1000 counters; oversize/duplicate
// discovery fails before swaps. New counters after discovery are excluded, and
// concurrent native traffic/writers remain possible. There is no scope snapshot,
// rollback or transaction. One reset per instance and eight globally reject
// contention, while independent reads remain allowed. One management deadline
// bounds the complete operation; a short deadline can leave partial effects.
//
// Before resetting, read reset-status for the Node epoch and control revision.
// Supply that epoch, an operation_id and explicit expected_revision. The same
// complete request retrieves its last evidence without performing another swap.
// A stale revision or another Node epoch rejects replay. Reset results include
// each identity, attempted adapter invocation, acknowledged old bytes (including
// zero), missing, known not_applied, unknown and not_attempted states. Operational
// errors use a normal response envelope on all transports; callers must inspect
// outcome/error/items. Preflight errors use ordinary transport failures.
//
// Reset-status remains readable during execution and after response loss. It
// pages at most 50 results, with a nonzero exact revision required after offset
// zero. Retention is one bounded operation (up to 1000 identities/receipts) per
// configured instance, without credentials, private native errors or a database.
// Starting another reset supersedes that evidence. An unresolved dispatched swap
// fences further resets in that local execution until explicit replacement;
// readback of current bytes cannot recover an unacknowledged previous value.
// Evidence is volatile and is lost on Node restart. The changed epoch prevents
// replay of the old request, but does not reconstruct its effects. Callers must
// never automatically refresh a revision/epoch to retry an ambiguous reset.
package traffic
