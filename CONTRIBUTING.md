# Contributing

Use Go 1.26+ and Git. Run `make check` and `make build` before submitting changes.

- Keep `cmd/commit` small: process lifecycle only.
- Construct commands in `internal/cli`; do not introduce global flag state or call `os.Exit` outside main.
- Put workflows in `internal/app` and declare interfaces at the consumer boundary.
- Thread contexts through subprocess and network operations; bound output and timeouts.
- Keep stdout useful for scripts and prompts/progress on stderr.
- Never push, create/update PRs, or commit before confirmation or explicit auto-accept.
- Preserve existing command/flag names. Document intentional safety behavior changes.
- Use local HTTP servers and temporary Git repositories in tests, never live APIs.
- Do not hardcode organization names, repository owners, or team-specific issue prefixes.
- Never include real credentials in fixtures, docs, or sample config.

Releases are opt-in. From a checkout of `github.com/myusuf3/commit`, tag a release and run GoReleaser. Review the draft release before publishing it. Configure `release_repository` only after a trusted public release exists.
