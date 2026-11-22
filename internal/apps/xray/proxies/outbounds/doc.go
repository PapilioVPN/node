// Package outbounds owns native outbound observations, live additions/removals,
// source selection and reviewed named-handler replacement on HTTP/gRPC/NATS.
//
// ListOutbounds returns at most 50 handlers per page. One native unary read is
// bounded to 32 MiB and 65536 decoded handlers; those ceilings do not cap Xray's
// allocations or protobuf heap usage. The native API has no cursor, targeted tag
// lookup or default readback. A retained observation supplies subsequent pages
// without refetching Xray. It expires after one minute, a new scan for that
// instance, or execution replacement. Native ordering and anonymous duplicates
// remain intact. Live changes do not rewrite an already retained observation;
// its original generation/time/source describe its freshness. Up to 32 scans
// share a 32 MiB retained configuration budget. Page bytes are copied so
// callers cannot alter retained observations. Full configurations may contain
// credentials; the existing trusted-controller boundary and no-store apply.
//
// Replacement plans accept one raw core.OutboundHandlerConfig or one selected
// outbound compiled from ordered native sources, with explicit default_policy
// preserve/may_change and connection_policy retain/terminate. Plan creation
// performs one fresh native list; source selection additionally compiles once.
// It does not construct a native handler or prove its connectivity. The plan
// exposes handler_not_constructed, the possible traffic gap, default uncertainty
// and the possibility of old resources remaining before any destructive action.
// Expire/comment are unused at this Xray pin and are rejected as effective edits.
// Named tag identity must remain the same; anonymous changes, source order and
// renaming belong to complete configuration review, not tag-only removal.
//
// Equal native tag/sender/proxy projection is an unchanged plan unless connection
// termination was requested. Live mode requires explicit acceptance of default
// changes and retained connections/resources. Native Remove never calls Close,
// never promotes another existing default, and can leave established flows alive.
// Native Add can assign a missing default even when duplicate registration then
// fails. Neither unordered listing nor the first returned row identifies default.
// An external writer can still change state between individual native calls.
//
// Apply consumes the plan identity once. It rechecks local generation/revision
// and one fresh target projection hash before ordered Remove then Add, sharing
// the existing eight global/one-per-instance mutation admission. There is no
// mutex over native I/O, atomic swap, automatic rollback, restart or replay.
// Readers use 32 global/eight per-instance slots and reject conflicting writes;
// continuation materialization uses the same admission. Successful live apply
// costs one preflight list plus two native mutations. Unchanged apply only reads.
// A stale, expired, superseded or changed-generation plan has no live effects.
//
// GetOutboundReplacement reads bounded, credential-free progress independently
// of native waits or execution fencing. Formed apply failures use normal result
// envelopes on every transport: inspect outcome/error and each remove/add step's
// attempted/effect/acknowledgement. An unknown effect cannot be treated as a known
// rollback. Replaying the same consumed plan returns its evidence without I/O.
// Dispatched unresolved failures fence the same execution through the existing
// control owner until explicit replacement. Pending candidates expire after one
// minute and share a 16 MiB budget; one candidate/result per instance is retained.
// A new plan supersedes the old evidence. Results are volatile across Node death.
//
// Preserve-default or terminate-connections plans route to configs_restart and
// cannot be applied by the live endpoint. This fallback terminates all old
// execution connections/resources, including when retain was requested. The
// controller reviews complete, original, ordered sources and uses the existing
// Configs ReplaceConfiguration
// with its required expected_revision (or ApplyConfiguration for the current
// desired revision). That path owns validation, durable publication and restart;
// its execution acknowledgement is spawning, not readiness. Explicit source order
// selects the desired startup default; unknown live default is not reconstructed.
// Outbound plans neither approve unrelated source changes nor pretend that a
// compiled selected handler is a lossless desired-source image. Callers inspect
// Configs publication/execution results and check readiness/traffic afterwards.
package outbounds
