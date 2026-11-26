# OpenVPN server

Node manages one configured OpenVPN server on Linux. The native integration suite uses OpenVPN 2.6.19.

```yaml
openvpn:
  id: primary
  executable: /usr/sbin/openvpn
  config_file: server.conf
  runtime_directory: /tmp
```

The runtime needs an OpenVPN executable, `/dev/net/tun` and the ability to manage the tunnel and routes in its Linux network namespace. Provide a separate OpenVPN binary if running Node in a container. The executable, config file and runtime directory must be visible to Node. Relative certificate and key paths inside the profile resolve from the configured profile directory. OpenVPN connects back to a Node-owned Unix socket in a private per-execution directory. Ensure any distribution confinement profile permits that connection. Use TLS with an API key or client CA for remote management.

Submit a server profile with client certificate verification. The managed profile supports TUN and native server, routing and TLS options. Keep process management, includes, plugins, external scripts and log output directives out of the profile. Node owns the process and management connection. A source is limited to 1 MiB, with each line shorter than 255 bytes to match the native reader.

| Operation | HTTP under `/v1/openvpn/instances/{id}` | gRPC |
| --- | --- | --- |
| Get status | `GET` on the instance resource | `OpenVPNInstancesService.GetStatus` |
| Start | `PUT /execution` | `OpenVPNInstancesService.Start` |
| Stop | `DELETE /execution` | `OpenVPNInstancesService.Stop` |
| Validate | `POST /config-validations` | `OpenVPNConfigsService.ValidateConfig` |

Start reads the configured file for an empty HTTP request or client omission. To propose different source bytes, set JSON string `config_text` in HTTP or the optional `config_text` bytes in gRPC. A present empty source is rejected. Validate accepts the raw profile as the HTTP body or the same optional field in gRPC. An omitted source validates the configured file. Rejected sources produce `valid: false` and a typed issue. The check covers managed directives and native option parsing. The certificate files, socket, port and tunnel are checked at startup.

Ready status includes `openvpn_version` and `applied_config_sha256`. The fingerprint is the lowercase SHA-256 of the exact source bytes, including whitespace. Referenced files have their own identities. A repeated source on a running execution returns `heartbeat`. A newly validated source restarts the instance and may interrupt traffic. A rejected source leaves the previous execution running. A launch failure after a restart can leave the instance failed. Status reports the last observed state without querying OpenVPN. Stop waits for the process to exit, and shutdown stops this Node's owned execution.

[Versioned gRPC contract](../internal/engines/openvpn/instances/bridge/grpc/proto/v1/openvpn_instances.proto)
