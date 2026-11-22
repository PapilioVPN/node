// Package credentials exposes typed native Xray credential tools for the pinned
// v26.11.9 executable. It selects a configured instance's binary and directory;
// the engine need not be running and no native management API is required.
// These operations generate or derive artifacts, without installation, engine
// mutations, desired-state writes, persistence, replay or automatic retry. Fresh
// randomness can be lost with a response; requesting again generates new material.
// UUIDv5 and supplied-key/seed derivations are explicitly deterministic.
//
// Operations are UUIDv4/v5, X25519, WireGuard, ML-DSA-65, ML-KEM-768, VLESS
// encryption pairs, TLS certificates, certificate SHA256 hashes and TLS ECH.
// Each successful request invokes one fixed native CLI command. There is no
// command, argv, environment, caller file path or arbitrary network target input.
// Ed25519/Lua are not registered by this pin. TLS ping belongs to diagnostics;
// Base64 presentation/encoding flags are represented by the typed artifact formats.
//
// Optional names/keys/seeds distinguish absence from a present empty value. UUID
// names are 1..30 UTF-8 bytes with the native zero namespace, not UUID strings. X25519 defaults to
// canonical unpadded base64url; its standard-base64 option and WireGuard use
// padded standard base64. Private keys decode to 32 bytes; native output is
// clamped. ML-DSA-65 and ML-KEM-768 seeds are canonical unpadded base64url of 32
// and 64 bytes. Returned public material and BLAKE3 Hash32 digests are checked
// against private material using Go crypto and the already pinned BLAKE3 library.
// VLESS returns both native authentication alternatives with their separate
// decryption/encryption strings, preserving native 600s and 0rtt defaults.
//
// TLS certificates accept at most 32 ASCII DNS names (wildcards allowed), optional
// UTF-8 common name/organization up to 1024 bytes, CA and validity. Omitted
// validity is native 90 days; explicit nanoseconds must be positive and at most
// 3650 days. Native generation is self-signed P-256. Its PEM private-key label
// says RSA PRIVATE KEY although the bytes are PKCS#8; original native material is
// retained and verified with Go TLS/X509. Generating a certificate neither assigns
// operator trust nor promises readiness, delivery or a public CA signature.
// Hashing accepts up to 64 KiB of PEM/concatenated DER certificates, at most 16,
// with ordered leaf index zero and later chain entries. Key blocks, trailing junk
// and control characters in native hash labels reject. Only this file-consuming
// command needs the instance's existing work_directory. Its owned 0700 staging
// directory and 0600 certificate are removed before return; caller paths and
// certificate/key export files are never accepted.
//
// ECH generation uses the native default cloudflare-ech.com or an explicit ASCII
// DNS public name. Restoration accepts canonical standard-base64 ECHServerKeys
// up to 32 KiB/16 key sets and cannot also select a new server name. Base64 and
// PEM output are supported; restored input is always base64, as in the native
// CLI. The supported KEM is X25519; the commented native PQ-ECH option is absent.
// Key/config association, native framing and cipher identifiers are checked.
// The pinned CLI concatenates separate ECHConfigLists for multiple restored keys.
// The typed response splits them into independently usable config_lists, without
// changing their config bytes/order or the server-key bundle. Native-generated
// config IDs are zero; callers combining external key sets own ID selection and
// client compatibility. Actual native-generated/restored lists pass Go TLS ECH
// handshakes, separately from proof of arbitrary client/protocol compatibility.
//
// One executor admits four operations globally and two per instance without a
// waiting queue. Native children have a five-second deadline, shortened by the
// request, an empty inherited environment and explicit working directory. They
// are killed/waited after cancellation, deadline, output excess or owner close.
// Native stdout is bounded to 128 KiB; stderr is discarded. Rejected or malformed
// native output returns no partial artifact. Cryptographic/framing checks occur
// inside admission. The 128 KiB JSON envelope, byte/count limits and deadlines
// do not establish hard engine/Go heap, filesystem latency or CPU sandbox bounds.
//
// Secret material is returned only in the current typed response. No credential
// receipt/status readback, logger or diagnostic exporter retains it. Errors carry
// only fixed public codes, without native text, argv, input, output or paths.
// Native -i restoration consumes arguments, so privileged host process inspection
// remains outside this API's confidentiality boundary. Go strings/crypto objects
// are managed memory; this contract does not promise complete memory zeroization.
// Existing controller mTLS, broker trust/ACLs and common request budgets apply.
// HTTP responses forbid caching. NATS broker/SDK queues and transport payload
// limits remain operator-owned and can be lower than this capability's limits.
//
// HTTP POST /v1/xray/tools/instances/:id/credentials/ creates uuids,
// x25519-keypairs, wireguard-keypairs, mldsa65-keypairs, mlkem768-keypairs,
// vless-encryption-pairs, tls-certificates, tls-certificate-hashes or ech-keysets.
// Bodies are strict JSON objects without instance_id. NATS uses the same fields
// plus required instance_id under xray.tools.credentials.<operation>.v1.generate
// (uuid, x25519, wireguard, mldsa65, mlkem768, vless, tls.certificates, ech), and
// tls.hashes.v1.get. Optional Papilio-Deadline only shortens the common timeout.
// Versioned CredentialsService RPCs expose the same service. JSON valid_for_ns
// and response duration_ns are exact decimal strings. Unknown/duplicate/null
// fields reject; absent optionals preserve native defaults. Generation inputs
// are name, private_key/encoding, seed, domains/common_name/organization/authority/
// valid_for_ns, certificate (base64 bytes), or server_name/server_keys/format.
//
// Shared integration is reserved for its owner: compose credentials.Module with
// configured instances and common requests in the tools group, then compose that
// group from xray.Module. The module owns its Fx providers, nine subscriptions,
// HTTP/gRPC registrations, stop cancellation/join and final io.Closer. Account for
// nine added subscription drains in the common shutdown budget. No new settings
// or native API binding is needed. Add tools/credentials/grpcapi/proto/v1/credentials
// (under internal/apps/xray/) to scripts/proto/generate.sh. Move the existing
// lukechampine.com/blake3 v1.4.1 requirement from indirect to direct; no new version
// is selected. Native integration runs the production Module through bootstrap's
// extension point. Normal group registration, generated-source CI/fuzz inclusion
// and final packaged acceptance remain shared integration work.
package credentials
