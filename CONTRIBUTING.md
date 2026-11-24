# Contributing to PapilioVPN Node

Thanks for helping improve Node. Bug reports, documentation, reproducible
benchmarks, and focused code changes are welcome.

## Choose a change

- Search [open issues][issues] and [pull requests][pulls] for existing work.
- For a bug, include the Node release or commit, Linux and engine versions,
  the operation and transport, steps to reproduce, and the expected and actual
  results. Use sanitized logs and synthetic credentials.
- For a new operation, engine integration, or public API change, open an issue
  first. Describe the operator need, the native API or tool involved, who owns
  the operation, and how you would test a successful result and relevant failures.
  Small fixes can go straight to a pull request.

## Open a pull request

1. Fork [PapilioVPN/node][repo] and start a short-lived branch from `main`.
   Use a descriptive name such as `fix/xray-account-replay` or `docs/quick-start`.
2. Keep the branch focused on one change. Use small commits with clear English
   messages and include the relevant tests or documentation.
3. Open a pull request against `main`. Summarize the user-visible change, link the
   issue if there is one, and list the checks you ran. Call out any changes to
   HTTP, gRPC, configuration, or deployment behavior.
4. Address review feedback and make sure the [Checks workflow][checks] passes
   before merge. Delete the topic branch once the work is merged.

`main` is the long-lived branch. Published versions use tags and [GitHub
Releases][releases]. Maintainers publish the package built and checked by CI
for the release commit.

## Follow the code conventions

- Keep binaries in `cmd/` and private Go packages in `internal/`. Put an
  operation, its native adapter, its transport handlers, and its tests with the
  capability that owns it. Use the existing Host or engine wiring for shared
  infrastructure.
- Keep request and result types explicit. HTTP and gRPC handlers should call
  the same operation. Preserve existing wire contracts, field presence, units,
  and error behavior unless the change proposes an intentional API revision.
- Prefer focused native operations and reuse supported Go libraries already in
  the project. Keep native I/O, concurrency, buffers, and waits bounded.
  Document ownership and cleanup for new resources.
- Write code and repository metadata in English. Run `gofmt` and keep private
  configs, keys, customer data, and local tooling files out of commits.

## Verify the change

Use the Go version declared in [go.mod](go.mod) and run the shared checks from the
repository root.

```bash
bash scripts/ci/check.sh
```

Put unit tests beside the code they exercise and cover cross-layer behavior in
the owning capability’s integration tests. For Xray, use the pinned standalone
binary from the [Checks workflow](.github/workflows/check.yml) and set
`NODE_TEST_XRAY` to its absolute path before running checks. Note any
environment-dependent checks you skipped. Run `bash scripts/ci/package.sh`
when changing runtime packaging or deployment behavior. It requires Linux,
Docker with Compose, curl, and jq.

If a protobuf contract changes, use `protoc` 36.2, `protoc-gen-go`
v1.36.12, and `protoc-gen-go-grpc` v1.6.0 for engine services. Run
`go generate ./internal/host/bridge/grpc` for Host or
`go generate ./internal/engines/xray/...` for Xray. For WireGuard, run
`go generate ./internal/engines/wireguard/...`. Include the schema and
source-relative generated files in the same change.

For performance changes, provide the workload, engine and tool versions,
machine details, command to reproduce, and before-and-after measurements for
the same operation and guarantees. If there is a comparable PasarGuard
operation, use the pinned reference for that workload. Put benchmark code and
results under the relevant capability in [benchmarks/](benchmarks/).

When adding or changing a public capability, update the affected sections of
the [English](README.md), [Persian](README.fa.md), [Russian](README.ru.md),
and [Simplified Chinese](README.zh-CN.md) READMEs together.

[repo]: https://github.com/PapilioVPN/node
[issues]: https://github.com/PapilioVPN/node/issues
[pulls]: https://github.com/PapilioVPN/node/pulls
[checks]: https://github.com/PapilioVPN/node/actions/workflows/check.yml
[releases]: https://github.com/PapilioVPN/node/releases
