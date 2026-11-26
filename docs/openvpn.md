# OpenVPN server

Node manages one configured OpenVPN server on Linux. The native integration suite uses OpenVPN 2.6.19.

```yaml
openvpn:
  id: primary
  executable: /usr/sbin/openvpn
  config_file: server.conf
  runtime_directory: /tmp
```

The runtime needs an OpenVPN executable, `/dev/net/tun` and the ability to manage the tunnel and routes in its Linux network namespace. The Node container image includes OpenVPN at `/usr/sbin/openvpn`. Install OpenVPN separately when running the native Node executable on a host. The executable, config file and runtime directory must be visible to Node. Relative certificate and key paths inside the profile resolve from the configured profile directory. OpenVPN connects back to a Node-owned Unix socket in a private per-execution directory. Ensure any distribution confinement profile permits that connection. Use TLS with an API key or client CA for remote management.

Submit a server profile with client certificate verification. The managed profile supports TUN and native server, routing and TLS options. Keep process management, includes, plugins, external scripts and log output directives out of the profile. Node owns the process and management connection. A source is limited to 1 MiB, with each line shorter than 255 bytes to match the native reader.

| Operation | HTTP under `/v1/openvpn/instances/{id}` | gRPC |
| --- | --- | --- |
| Get status | `GET` on the instance resource | `OpenVPNInstancesService.GetStatus` |
| Start | `PUT /execution` | `OpenVPNInstancesService.Start` |
| Stop | `DELETE /execution` | `OpenVPNInstancesService.Stop` |
| Validate | `POST /config-validations` | `OpenVPNConfigsService.ValidateConfig` |
| List sessions and counters | `GET /sessions` | `OpenVPNOperationsService.ListSessions` |
| Disconnect a session | `DELETE /sessions/{session_id}?execution_id=…` | `OpenVPNOperationsService.DisconnectSession` |
| Follow live logs | `GET /logs` | `OpenVPNOperationsService.GetLogs` |
| Read signed revocation list metadata | `GET /revocation-list` | `OpenVPNAccessService.GetRevocationList` |
| Replace the signed revocation list | `PUT /revocation-list` | `OpenVPNAccessService.ReplaceRevocationList` |

Start reads the configured file for an empty HTTP request or client omission. To propose different source bytes, set JSON string `config_text` in HTTP or the optional `config_text` bytes in gRPC. A present empty source is rejected. Validate accepts the raw profile as the HTTP body or the same optional field in gRPC. An omitted source validates the configured file. Rejected sources produce `valid: false` and a typed issue. The check covers managed directives and native option parsing. The certificate files, socket, port and tunnel are checked at startup.

Ready status includes `openvpn_version` and `applied_config_sha256`. The fingerprint is the lowercase SHA-256 of the exact source bytes, including whitespace. Referenced files have their own identities. A repeated source on a running execution returns `heartbeat`. A newly validated source restarts the instance and may interrupt traffic. A rejected source leaves the previous execution running. A launch failure after a restart can leave the instance failed. Status reports the last observed state without querying OpenVPN. Stop waits for the process to exit, and shutdown stops this Node's owned execution.

[Versioned gRPC contract](../internal/engines/openvpn/instances/bridge/grpc/proto/v1/openvpn_instances.proto)

## Live sessions and logs

`ListSessions` reads one native status snapshot with session ID, certificate common name, remote endpoint, assigned tunnel IPs, negotiated cipher, connection time, and bytes received and sent from the server's perspective. The counters are scoped to each active session. JSON encodes session IDs and byte counters as decimal strings. Times are Unix seconds.

Pass the snapshot's `execution_id` and the target session ID to disconnect. OpenVPN confirms the disconnect command. A subsequent snapshot can confirm that the session disappeared. OpenVPN can reconnect clients whose certificates remain authorized. Node rejects session IDs from a previous execution. The management socket accepts one command at a time, and concurrent operations may receive a busy response.

HTTP logs use newline-delimited JSON batches. gRPC logs use server streaming. A new stream starts with the retained logs. Resume with `execution_id` and the last `after_sequence` for that execution. Node retains the latest 128 records and includes `dropped` when a client falls behind. Log streams end when the execution exits.

## Managed certificate revocation

Set both `revocation_ca_file` and `revocation_list_file` to enable managed revocations. Use a PEM CA certificate with certificate-revocation signing enabled. The CRL path needs a writable parent directory for Node and must be readable by the OpenVPN user. With this setting Node supplies the CA and CRL paths. Remove the profile's `ca`, `capath` and `crl-verify` directives before enabling it.

Submit an initial signed PEM CRL before Start. Send the raw PEM body over HTTP or `signed_pem` bytes over gRPC. Node verifies the issuer, signature, validity dates and monotonically increasing CRL number, then atomically replaces the file. A byte-identical retry is safe. Read the metadata after a lost acknowledgment to verify the saved revision. Node keeps the CRL on disk across execution stops and process restarts.

OpenVPN reloads the file for new TLS handshakes. To remove an active connection immediately, publish the revocation first and then disconnect the affected session. Core owns certificate issuance and the decision to revoke.
