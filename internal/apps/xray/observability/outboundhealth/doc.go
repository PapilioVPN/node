// Package outboundhealth owns existing native outbound health evidence.
//
// The pinned ObservatoryService has one complete unary read and no exact-tag,
// cursor, limit or manual-probe operation. Both Get and an initial List perform
// one native RPC. List continuations perform zero native RPCs. Reads neither
// configure the observatory nor generate network probes. Missing status means
// not observed, not absent from the outbound manager or healthy/offline.
//
// Ordinary observations retain last-attempt and last-success Unix seconds. The
// sample timestamp is the last attempt; freshness compares its wall-clock age
// with max_sample_age_seconds (zero selects 60, maximum 86400). A failed ordinary
// probe's delay sentinel remains raw delay_ms with no latency_ms. Burst retains
// all six signed health-ping statistics (durations in nanoseconds), but the native
// service has no burst sample timestamps. Its freshness therefore stays unknown.
// Future/invalid timestamps also have unknown freshness; raw integers remain.
// Node observed_at records receipt of evidence, never probe execution time.
// Source identifies the native API; mode identifies ordinary versus burst from
// the pinned health_ping submessage. Private native error reasons are omitted.
//
// Native replies are bounded to 1 MiB before real gRPC decode and 4096 records
// afterwards. At most 32 native reads globally and eight per instance are admitted
// without waiting. Exact selection scans O(N); collection sorting costs O(N log N).
// A page contains at most 50 records. Retained continuations account for tags and
// Go records within an 8 MiB/eight-observation budget, one observation per instance.
// They expire after 30 seconds, are swept on access/new lists, and do not survive
// Node restart. A new successful initial list supersedes that instance's previous
// continuation. Invalid scope, changed execution, expiry or supersession rejects
// continuation. Continuations preserve native membership/observed_at while their
// served_at and sample freshness advance. Pages can be repeated until expiry.
// Native observations are not atomic snapshots and local generation is not
// endpoint/process attestation. Byte/count/retention budgets are not hard native
// engine or protobuf heap/CPU limits. Configured transport/broker limits may be
// lower than the supported response (a worst-case escaped JSON page is <2 MiB).
//
// HTTP uses GET /v1/xray/observability/instances/:id/outbound-health/status with
// required outbound_tag (present empty selects an anonymous tag), and /statuses
// with optional page_size/page_token. Both accept max_sample_age_seconds. HTTP
// forbids caching. NATS uses xray.observability.outboundhealth.v1.get and .list
// under the configured prefix, with the same JSON fields plus instance_id.
// gRPC exposes OutboundHealthService in papilio.xray.observability.outboundhealth.v1.
// All transports consume the same application service and existing controller
// mTLS/broker authority, with common deadlines and sanitized errors. JSON int64
// and uint64 fields are decimal strings; absence is null/message absence.
// api_not_configured identifies absent Node API configuration. unsupported means
// the native method is not registered (which can include a disabled observatory
// service); an empty configured selector is an empty successful observation.
// Error text cannot distinguish all native feature-disable causes safely.
//
// Shared integration is intentionally left to the composition owner. Add
// outboundhealth.Module(requests) to xray/observability.Module. In the instance
// module export services.StatusReader for this capability from the existing
// instance service, just as for statistics. Add
// internal/apps/xray/observability/outboundhealth/grpcapi/proto/v1/health to the
// scripts/proto/generate.sh schema list. There are two additional NATS drains;
// account for them in the common shutdown budget. No new dependency/settings or
// per-capability permissions are required. Capability integration tests use the
// normal bootstrap extension point with those explicit Fx bindings; shared
// registration, final packaged runtime and release benchmarks remain integration
// acceptance until those shared files are updated by their owner.
package outboundhealth
