// Package routing owns the pinned Xray RoutingService operations. HTTP, gRPC
// and Core NATS adapt the same application service. The native channel belongs
// to runtime/instances; the routing adapter never closes it.
//
// Rule additions prepare one native JSON rule through the existing instance
// configuration compiler, then make one AddRule call. Append preserves existing
// precedence. Replace requires both mode=replace and confirm_destructive=true;
// it replaces the rule slice and removes existing balancers and overrides. It
// does not replace domainStrategy or an already initialized routing script.
// Builder acceptance cannot establish runtime condition acceptance. Generic
// native errors can follow partial effects and are reported as unknown.
//
// These live changes are ephemeral. There is no desired-state write, automatic
// replay, rollback, outbound-existence check or native CAS. Native RemoveRule
// acknowledges missing tags and does not terminate existing connections. An
// override target must be explicitly present; present empty clears it. Native
// overrides can name nonexistent outbounds or targets outside the selector.
//
// TestRoute invokes native selection, including possible DNS, balancer strategy
// advancement and webhook effects. Selectors are native field-name prefixes;
// omitted projections carry native zero values, including VLESSRoute, which is
// never projected by this pin. traffic_verified=false means that no traffic was
// verified by this operation. Real payload acceptance is an independent test.
// Stock v26.11.9 registers no routing-stats channel. Control status explicitly
// reports disabled_stock_xray; no substitute event stream is advertised.
//
// Rules use one bounded native ListRule, preserving order, empty tags and
// duplicates. Pages default to and never exceed 50. At most eight observations
// of up to 65536 rules and 4 MiB of estimated rule storage are retained for 30
// seconds; admission also reserves pending initial reads. Continuation performs
// no native I/O, preserves the original observation and page size, and fails on
// expiry or an execution change. Mutations do not turn old pages into current
// state. Initial decoding is bounded to 32 MiB; these limits do not cap Xray's
// native list allocation or scan. Balancer principal targets preserve absence
// and explicit empty, or fail resource_exhausted above 50 without truncation.
//
// Routing admits 32 reads globally, eight per instance, and eight effectful
// requests globally, one per instance. Conflicting reads and effects reject
// immediately. No state lock spans native work. A dispatched unresolved error
// fences further native reads/effects in that execution. Local control evidence
// remains readable. Explicit lifecycle replacement clears the effective fence;
// late native completion does not. Last evidence is volatile and cannot recover
// operation identity after Node death or establish delivery to a controller.
//
// Module consumes xray-native-api Fx bindings, services.StatusReader and the
// optional configuration.Compiler. The integration owner must export the latter
// two from runtime/instances and runtime/configs, compose this module through
// xray/network and xray, add its proto/v1/routing schema to scripts/proto inputs,
// and account for seven new NATS subscription drain budgets. Tests install those
// aliases through bootstrap.New's extra Fx options without modifying shared
// files. Default application installation and packaged release acceptance remain
// the integration owner's checks.
package routing
