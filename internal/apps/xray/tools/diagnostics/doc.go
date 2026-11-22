// Package diagnostics provides a bounded, shareable report for one configured
// Xray instance. GET /v1/xray/tools/instances/:id/diagnostics,
// DiagnosticsService.GetInstanceDiagnostics in papilio.xray.tools.diagnostics.v1,
// and NATS <prefix>.xray.tools.diagnostics.v1.get share the same application service.
// Input is instance_id and optional max_age_seconds (default 60, maximum 86400).
// HTTP accepts that option as a query and forbids bodies/caching. The existing
// controller mTLS/broker ACL boundary and request deadlines apply to all adapters.
//
// The report reads current Node execution twice and configuration metadata once.
// Desired revision, durability and update time are separate from the revision
// actually selected by the execution owner. An old configuration update time
// does not make a fresh authoritative metadata read stale. matches_applied is
// absent without fresh execution/revision evidence. These reads are independent,
// not a transaction. Generation/state changes mark native evidence stale and
// retain its original generation; stale/absent/future source times never become
// fresh merely because the report was received now. No diagnostic cache exists.
//
// The configured executable receives only the fixed version argument with an
// empty inherited environment. Its allowlisted version/build/Go/platform fields
// describe that executable, not necessarily the running process. Unknown banner
// formats fail closed; stdout/stderr and private paths never enter the report.
// A version command is not a filesystem/process sandbox or endpoint attestation.
//
// Native inspection borrows the existing instance channel. One GetSysStats and
// one v1 reflection stream (one ListServices request/reply) are the complete native
// work, with no retries, mutation probes, user/handler/config inventory or outbound
// network probes. Runtime verification covers GetSysStats only. The five fixed
// capability rows project native service registrations; advertised is not proof
// of every method/feature. In particular stock routing events remain disabled and
// generic AlterOutbound has no concrete operation in the pinned engine. Unknown
// reflected service names are never exported. Missing Reflection leaves other
// capabilities unknown, not disabled. Native Unimplemented means unsupported or
// unregistered; it cannot safely distinguish every disabled native feature.
//
// Known absent Node API/desired-state configuration is disabled. Missing bindings
// or failed sources are unavailable. Operational faults form per-section findings
// so working evidence remains useful; canceled/expired outer requests, unknown or
// invalid selection and whole-report overload return ordinary sanitized errors.
// Cause errors stay internal. Projection includes no original sources, hashes,
// paths, endpoints, credentials, visitor identities, logs or private error text.
// Instance ID and the documented numeric/version facts are intentional disclosures.
//
// Four whole reports globally and one per instance reject overload without queues.
// Version children share two slots, at most one second and 8192 output bytes each.
// Each native check and metadata read has a 250ms cooperative subdeadline, shortened
// by the caller. Native frames are at most 64 KiB, reflected services at most 64
// after decode, and exported capabilities exactly five when checked. Both JSON
// and protobuf responses are capped at 32 KiB. Filesystem/engine internals and
// protobuf heap are not hard resource bounds. Child commands are killed/joined
// and reflection streams are canceled/closed before capacity is released.
//
// Shared integration, reserved for its owning session: compose
// diagnostics.Module(instances, requests) through the new tools group and add that
// group to xray.Module. Export this capability's services.StatusReader from the
// existing runtime/instances service and services.MetadataReader from the existing
// runtime/configs StateService. Add tools/diagnostics/grpcapi/proto/v1/diagnostics
// (under internal/apps/xray/) to scripts/proto/generate.sh. Account for one added
// NATS subscription drain in the common shutdown budget. No new dependency or
// settings are needed. Integration tests run the production Module through the
// bootstrap extension point with real canonical process, lifecycle and config
// owners. Normal group registration and final packaged acceptance follow shared
// integration; these files intentionally do not modify those shared owners.
package diagnostics
