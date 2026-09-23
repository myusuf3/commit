# commit

An AI-powered Git commit and GitHub pull request assistant, built as a conventional Go CLI.

## Install

Requires Go 1.25+ to build and Git at runtime. No GitHub CLI is required.

```sh
go install github.com/myusuf3/commit/cmd/commit@latest

# Or build from a local checkout:
go build -o bin/commit ./cmd/commit
./bin/commit --help
# Or install into GOBIN (defaults to GOPATH/bin):
go install ./cmd/commit
```

Source and module: [`github.com/myusuf3/commit`](https://github.com/myusuf3/commit). Self-updates still require an explicitly configured, trusted release repository; ordinary commands never check for updates.

## Same command interface

```sh
commit init

# Review staged changes, generate a message, then confirm the commit:
git add path/to/file
commit commit
commit commit --auto-accept      # -y

# Create or update a PR for the current branch:
git fetch origin
commit pr
commit pr --draft                # -d
commit pr --issue 123,TEAM-456    # -i; repeated flags also work
commit pr --auto-accept          # -y; approves any necessary push too

commit version
commit --version
commit update --check            # -c
commit update --force            # -f
```

The original command names, flags, and short options are preserved; the executable is named `commit`. The `commit commit` spelling is intentional. Additional options are additive:

```sh
commit commit --dry-run > message.txt
commit pr --dry-run --issue OPS-42 > pull-request.md
commit pr --no-browser
commit --config /path/to/config commit
commit completion bash          # also zsh, fish, powershell
```

### Behavior and safety

- **Preview before mutation.** Both commit and PR operations show generated content first. Blank confirmation means **no**. PR approval covers the displayed push and create/update plan. `--auto-accept` skips all prompts, including optional issue entry.
- **Non-interactive use is explicit.** Without a terminal, use `--auto-accept` or `--dry-run`. EOF cancels instead of accepting or looping forever.
- **Dry run does not commit, push, modify PRs, fetch refs, or launch a browser.** It still queries APIs/Git remotes and sends the diff to the configured AI service; normal API billing applies.
- **Staged changes only** for commits. PR descriptions use **committed changes** against the merge base of `origin/<default-branch>` and HEAD; unstaged/staged-but-uncommitted changes are not included. Run `git fetch origin` yourself to refresh the base. No silent fetches or background update checks.
- **Pushes are non-forced and only happen after approval.** Network errors are not treated as missing remote branches. A different or multiple origin push URLs require a manual push first. Cross-repository/fork PRs and GitHub Enterprise are not currently supported.
- **Existing PRs** matching the current branch and default base are updated instead of duplicated. Existing recognized issue-closing links are retained when no issues are supplied. Explicit `--issue` values replace that set; invalid or explicitly empty flag values fail validation. Blank interactive input preserves existing links. `--draft` does not change an existing PR's draft status. Updating replaces its title/body, so review the preview to preserve any hand-written notes.
- **Issue support is organization-neutral.** GitHub numbers produce `Fixes #123`; any team key such as `TEAM-456` produces `Closes TEAM-456`. References are deduplicated and appended to the title in brackets.
- **Large diffs fail clearly**, rather than being silently truncated or rearranged. Set `max_diff_bytes` appropriately for your model; this byte limit is not a token estimate.
- Commits recheck the symbolic HEAD target (including attached/detached state), HEAD hash, and staged diff. PRs recheck the branch, HEAD hash, and origin before mutation. This reduces stale-plan mistakes but is not a transaction: avoid concurrent Git operations, and remember hooks can modify a commit. A pushed branch cannot be rolled back automatically if the subsequent GitHub API call fails.
- Generated content is treated as untrusted: PR JSON is validated, terminal control characters and text-reordering Unicode (bidi overrides, zero-width space, BOM; ZWJ/ZWNJ are allowed so Persian, Arabic, Indic, and emoji text still works) are rejected, and messages are passed to Git via stdin, never a shell. Always review output; prompt instructions are not a guarantee of factual accuracy or secret removal.
- **Remote credentials.** HTTPS origin URLs may embed credentials, as in some CI checkouts. Only `owner/name` is passed to the GitHub API adapter; raw remote URLs are not printed, and Git handles its own transport authentication. Fetch/push comparison ignores **only HTTPS userinfo** so tokens can rotate. SSH usernames and other destination components must still match. URL queries and fragments are rejected; multiple push URLs are refused. Pushes use Git's own transport, are never forced, and use a longer fixed 15-minute bound than other Git commands because transfers are large.
- Progress/prompts go to stderr. Generated text, version information, and PR URLs go to stdout. Output uses no ANSI colors. PRs open in a browser only during interactive use; `--no-browser` disables it.

## Configuration

`commit init` interactively creates a private, non-overwriting configuration file:

1. `--config PATH`
2. `COMMIT_CONFIG`
3. `$XDG_CONFIG_HOME/commit/.commitrc` when XDG_CONFIG_HOME is set (must be absolute)
4. `~/.config/commit/.commitrc`

There is deliberately **no automatic repository-local configuration discovery**: an untrusted checkout must not redirect your API credentials. Missing default config is fine when environment variables provide credentials. An explicitly selected missing file is an error. Unknown TOML keys are rejected.

```toml
provider = "openai"          # openai or opencode-go
model = "gpt-4o-mini"
base_url = "https://api.openai.com/v1"
timeout = "60s"
max_diff_bytes = 100000
# api_format = ""             # auto-detected; override with chat-completions, messages, or responses

# Prefer environment variables instead of storing secrets here.
# api_key = "..."
# github_token = "..."

# Set only after publishing releases to a repository you trust.
# release_repository = "owner/repo"

[conventional]
type_scope_prefix = true
```

Environment variables override file settings:

| Variable | Setting |
| --- | --- |
| `OPENAI_API_KEY` | API key |
| `COMMIT_API_KEY` | API key, takes precedence over OPENAI_API_KEY |
| `GITHUB_TOKEN` | GitHub token |
| `COMMIT_GITHUB_TOKEN` | GitHub token, takes precedence over GITHUB_TOKEN |
| `COMMIT_PROVIDER` | `openai` or `opencode-go` |
| `OPENCODE_API_KEY` | OpenCode Go API key |
| `OPENCODE_GO_API_KEY` | OpenCode Go API key (alias; takes precedence) |
| `COMMIT_API_FORMAT` | `chat-completions`, `messages`, or `responses` |
| `COMMIT_MODEL` | Model |
| `COMMIT_BASE_URL` | OpenAI-compatible API base URL |
| `COMMIT_RELEASE_REPOSITORY` | Explicit update source, `owner/repo` |

An explicitly empty credential environment variable clears the file value. GitHub credentials are required only for PR operations, not for commit generation or initialization. Git pushes use Git's own SSH/credential-helper authentication. Initialization does not copy environment credentials into the file, hides typed secrets on terminals, and writes the config with mode `0600` (directory `0700`; Windows uses its native permission semantics). Existing files and symlinks are never overwritten.

HTTPS is required except for loopback hosts, for example `http://localhost:1234/v1`; providers without authentication can use a dummy API key. The configured timeout applies per HTTP request and Git subprocess (except pushes, which get 15 minutes), not to time spent reviewing a prompt. Ctrl-C, SIGTERM, and deadlines cancel work. Git subprocess cancellation targets its process group on Unix, and a Job Object on Windows; Windows starts Git suspended and assigns the job before resuming it so helpers cannot escape during startup. This is not a sandbox: deliberately daemonized Unix helpers can leave the group, and cancellation cannot undo side effects that already happened. If Windows cannot set up containment, Git is not allowed to run uncontained.

### OpenCode Go

[OpenCode Go](https://opencode.ai/docs/go/) is a subscription that exposes coding models through three different API shapes. Select it and the client picks the right one per model:

```sh
# Config file
provider = "opencode-go"
model = "kimi-k2.6"      # bare model ID, not opencode-go/<model>

# Or environment only
export COMMIT_PROVIDER=opencode-go
export OPENCODE_API_KEY=...
export COMMIT_MODEL=kimi-k2.6
```

Routing follows the published model families and can be overridden with `api_format` (or `COMMIT_API_FORMAT`) for new or self-hosted models.

| Model IDs | API format | Endpoint |
| --- | --- | --- |
| `glm-*`, `kimi-*`, `deepseek-*`, `mimo-*`, `longcat-*`, `hy3`, `hy4-*` | `chat-completions` (default) | `/chat/completions` |
| `minimax-*`, `qwen*` | `messages` | `/messages` |
| `grok-*`, `gpt-*`, `muse-spark-*` | `responses` | `/responses` |

Using the wrong format for a model fails with a clear error rather than silently returning nothing, and `opencode-go` refuses to send Go credentials to the OpenAI endpoint (and vice versa). The `x-opencode-session` header OpenCode asks clients to send is attached to every request in one command invocation so routing and prompt caching stay stable, and a non-generic `User-Agent` is always sent. Responses API requests set `store: false`. A `COMMIT_PROVIDER` switch deliberately discards the previously stored API key, `base_url`, `api_format`, and model so credentials never leak across providers.

**Privacy:** diffs can contain proprietary code and secrets. OpenCode Go model retention varies — some models retain data for up to 30 days and one contributor tier trains on prompts and completions — so confirm the specific model's terms before sending sensitive diffs. Inspect what you stage and the committed branch diff before invoking generation. This app does not provide automatic secret detection. Use a trusted provider/endpoint and keep config credentials outside version control.

## Updates and releases

`commit update` retains its command interface but intentionally has no default upstream. Set `release_repository` or `COMMIT_RELEASE_REPOSITORY` after trusted releases are published. `--check` only reports availability; `--force` permits replacing a dev build or reinstalling the latest release (including a downgrade from a newer build).

The updater requires `commit_<os>_<arch>.tar.gz` (`.zip` on Windows) and `checksums.txt`. SHA-256 verification is mandatory. Archives, responses, and extracted binaries are bounded; only the exact binary entry is accepted. On macOS/Linux, a same-directory temporary binary must execute `version` successfully before atomic replacement. Failure before replacement leaves the old executable untouched. On Windows, use `--check` and install the archive manually because a running executable cannot safely replace itself.

Checksums protect integrity, not against a compromised publisher. Only configure repositories you trust. Asset downloads support public GitHub releases; private release downloads are not implemented. API requests can use a token, but it is never forwarded to asset redirects. There are no periodic or automatic update checks.

`.goreleaser.yaml` provides cross-platform archives and checksums without a fixed repository owner. It uses the `formats` key, so GoReleaser v2.6 or newer is required. From a tagged Git checkout with the intended `origin`, run `goreleaser release --clean`; use `goreleaser release --snapshot --clean` for local packaging. No publish workflow is enabled by default.

## Development

```sh
make build
make test       # go test -race ./...
make vet
make fmt
```

```text
cmd/commit/        process entry point, signals, build metadata
internal/cli/      Cobra constructors, flags, prompts, stream handling
internal/app/      workflows and consumer-owned adapter interfaces
internal/config/   strict TOML, environment overrides, private file creation
internal/git/      cancellable Git subprocesses
internal/llm/      validated OpenAI-compatible generation
internal/github/   GitHub REST and strict remote parsing
internal/httpapi/  bounded HTTP transport helpers
internal/update/   explicitly configured, checksum-required updates
```

Tests use temporary Git repositories, injected adapters, and local HTTP servers—no real credentials, AI requests, pushes to hosted repositories, or PR creation. CI runs tests/vet on Linux, macOS, and Windows. See [docs/review.md](docs/review.md) for the review findings and design choices.

## License

MIT. See [LICENSE](LICENSE).
