// Package online exposes transient Xray presence through HTTP, gRPC and Core
// NATS. Controller trust and deadlines are supplied by the installed transports.
//
// Count targets one native map; zero is tracked/offline, absent is untracked.
// Native reads cannot distinguish a never-used/missing email from disabled
// statsUserOnline policy. They never establish account membership or per-inbound
// attribution. A missing configured API, unsupported RPC and unavailable/stopped
// engine are separate errors, never successful offline observations. Empty online
// collections likewise do not prove the tracking policy is enabled.
//
// ListOnlineUsers uses one GetAllOnlineUsers, preserving full opaque emails.
// ListOnlineRecords uses GetAllOnlineUsers plus GetUsersStats(include_traffic=false,
// reset=false), with no per-user requests. The pinned native detailed method cuts
// emails at >>> and merges colliding prefixes. Known such identities cause
// native_identity_unsupported for the whole detail observation; names and exact
// IP reads still work. The guard and details are separate non-atomic observations:
// concurrent external membership changes can invalidate the selection, and a
// newly introduced colliding prefix cannot be attested by this native facility.
// No traffic projection is offered: native zero traffic fields cannot distinguish
// absent counters from known zero, and presence does not need a counter scan.
//
// HTTP GET /v1/xray/users/instances/:id/online/users and /online/records accept
// page_size and page_token. The existing /online/ips additionally requires email
// and now supports the same paging options. The versioned OnlineService provides
// ListOnlineUsers, ListOnlineRecords and ListUserOnlineIPs. NATS suffixes are
// xray.users.online.v1.list, xray.users.online.records.v1.list and the existing
// xray.users.online.ips.v1.list; strict JSON requires instance_id, optional integer
// page_size/string page_token, and email for IPs. Shared controller authority,
// request deadlines, no-store HTTP and private native diagnostics remain intact.
//
// Size zero selects 50, explicit sizes are 1..50. Names count email rows; detailed
// collections count flattened email/IP rows, so one email's large IP set is also
// paged. Exact IP pages preserve online/offline/untracked state for the complete
// native result. Native last_seen is last AddIP Unix seconds, not an activity
// heartbeat. Final reference removal erases the IP and its last-seen timestamp.
//
// Each initial request reads the entire native collection once (two fixed bulk
// calls for details), sorts once and retains an immutable observation if needed.
// It returns at most 50 rows. Continuations make zero native calls; observation ID,
// generation, observed_at and expires_at remain fixed, served_at advances and fresh
// is false. This is bounded retention of a non-atomic native observation, not live
// data on subsequent pages. Tokens bind instance, operation, email and page size.
// Replay within the lifetime is stable; clients need not consume pages in order.
// New observations require omitting page_token. No native snapshot/cursor exists.
//
// Retention expires absolutely after 30 seconds, including slow consumers; it is
// never silently replaced with a fresh read. Expired/unknown tokens return
// observation_expired. A local generation change returns execution_changed; a
// stopped engine returns engine_unavailable. Node restart loses all tokens. Local
// generation does not attest endpoint identity or detect external engine restarts.
//
// Global pending plus retained observations are limited to eight, with immediate
// resource_exhausted and no eviction of an unexpired traversal. Each observation
// admits at most 65536 rows and 4 MiB logical retained content (text bytes plus
// per-row allowance). Each native unary reply is limited to 4 MiB before decoding.
// Limits fail the whole observation without partial success. The existing 32
// global/eight per-instance read admission also applies to first-page native work;
// continuations only inspect local status and copy at most 50 retained rows.
// Expired storage is reclaimed lazily on the next page operation; there is no
// background worker or persistence. Logical retention limits and native frame
// limits are not hard Go/protobuf heap, engine CPU/RAM or serialization bounds.
// Initial mapping/retention is O(rows + text bytes), sorting O(N log N); continued
// page copying is O(page rows). Transport and broker payload limits can be lower.
package online
