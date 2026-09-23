# Local validation before publication

Validated on macOS/arm64 with Go 1.26.7, and the test suite also passed with Go 1.25.13.

Passed:

- `go test -race -count=1 -cover ./...`
- `go vet ./...`
- `go mod verify`
- `govulncheck ./...` — no vulnerabilities found
- `GOTOOLCHAIN=go1.25.13 go test ./...`
- `gofmt` clean (`gofmt -l cmd internal` empty)
- Native build, `--help`, and config-error smoke checks
- CGO-disabled cross-builds of the CLI and Git test binary for Linux, macOS, and Windows, each on amd64 and arm64
- Windows-targeted `go vet ./...`
- Shuffled CLI/config tests under deliberately conflicting dummy credential, provider, endpoint, and config-path environment variables
- Source, configuration, documentation, and dependency-path audit for previous branding

Tests include actual temporary Git repositories and complete command flows using local mock endpoints. Coverage exercises approval/dry-run boundaries, initial and staged-only commits, stale state rejection, issue normalization, request payloads, HTTP errors/timeouts/redirects, output limits, config permissions/non-overwrite behavior, update checksums/extraction, and failed-install preservation.

Follow-up coverage checks: credential-bearing remote URLs parse to a repository path without leaking the credential, push-destination comparison tolerates a rotated token but rejects a different repo/host/multiple URLs, empty `--issue` fails before any generation, and Unicode validation still accepts Persian/CJK/emoji text while rejecting ANSI escapes, bidi overrides, and other invisibles.

Review-fix regression coverage:

- Actual Git branch switching/renaming and attaching/detaching at the same commit are rejected without consuming staged changes. Unchanged detached and unborn commits still succeed.
- On macOS, cancellation and deadline expiry terminate nested subprocesses, verified by closure of a descendant-owned loopback socket. A real pre-commit hook regression exercises the Git integration.
- HTTPS token rotation is accepted, but SSH username changes, URL queries/fragments, malformed URLs, and multiple push destinations are rejected. Local push paths containing spaces work.
- CLI environment isolation restores the parent environment, and inherited dummy `COMMIT_API_KEY` no longer contaminates the OpenCode fixture.
- Blank issue parsing returns `nil` and preserves links. The earlier data-loss claim was incorrect; explicit empty-flag rejection is validation only.

Windows Job Object containment is implemented, cross-compiled, and statically checked; the shared descendant-cancellation tests are included for Windows CI but have not been executed on Windows locally.

OpenCode Go coverage additionally checks model→format routing, that the Messages endpoint uses `x-api-key`/`anthropic-version` with no bearer token, that a stable `x-opencode-session` header is present for Go and absent for OpenAI, `store: false` on Responses requests, rejection of truncated/refused/non-text output, provider-key isolation, provider-switch credential discarding, and the OpenAI-endpoint guard.

Not performed:

- Live AI-provider requests, including OpenCode Go (no real credentials used)
- Hosted GitHub pushes or PR creation/update
- Execution of cross-built Linux/Windows binaries
- Hosted CI execution
- GoReleaser packaging or publishing (GoReleaser is not installed locally)
- A live self-update against a published release

The original source repository was left untouched. These results describe local validation before publication; they do not establish hosted CI or release results.
