# Review and redesign

This is a separate implementation with the same command interface, not an in-place migration. The original working tree is untouched.

## Findings addressed

| Finding in the reviewed implementation | New behavior |
| --- | --- |
| Package-global Cobra commands, flags, and printers; `os.Exit` in command handlers | Fresh command constructors, injected adapters/streams, `RunE`; process exit only in `cmd/commit/main.go` |
| PR branch pushes before generation and user review | Read-only preparation followed by one visible confirmation covering push and create/update |
| New PR creation bypassed confirmation | Preview and confirmation apply to both creation and updates |
| Auto-accept could still prompt for issue numbers | Auto-accept is fully non-interactive |
| Several buffered readers competing for stdin; initialization loops on EOF | One shared reader, explicit EOF cancellation, private terminal secret entry |
| Configuration documentation and discovery behavior disagreed | One documented lookup policy, explicit paths and environment overrides, no ambient checkout config |
| Manually interpolated secrets into TOML | TOML encoder and exclusive private file creation; no overwrite/symlink following |
| Organization-specific issue prefixes and duplicated parsing | Centralized parsing for any team key, validation, normalization, deduplication |
| No HTTP deadlines or request cancellation; unbounded response reads | Per-request timeouts, contexts, bounded bodies, redirect refusal on authenticated APIs |
| Provider responses parsed without reliable HTTP status handling | Status-specific errors without reflecting provider bodies or keys |
| PR title/body inferred from the first newline | Validated JSON payload; the model cannot supply PR IDs or API metadata |
| Diff reduction separated file headers from hunks and guessed model token budgets | Fail on a configurable byte limit, preserving complete diffs and file/hunk relationships |
| External Git diff helpers could execute during preview | `--no-ext-diff --no-textconv --no-color` |
| Git errors conflated with missing remote branches; stale tracking refs used for push detection | Real `ls-remote` query, explicit error propagation, current remote SHA comparison |
| Remote URL parsing accepted host substrings | Exact host/scheme checks and strict two-component repository paths |
| No check that generated metadata still matched Git state | Recheck symbolic HEAD/hash/index diff for commits, or branch/HEAD/origin for PRs, before applying |
| Automatic update checks on ordinary commands and a baked-in release source | Explicit `update` command and configured trusted source only |
| `--force` existed but was ineffective | Implemented forced reinstall/dev-build replacement |
| Checksums optional; executable verification only checked file mode | Mandatory checksum, bounded exact-entry extraction, actual `version` execution, atomic replacement on Unix |
| Some tests duplicated production logic instead of exercising it | Actual adapters tested with temporary repositories/local HTTP servers; workflows tested through public methods/commands |
| Documentation claimed unsupported functionality or dependencies | Documentation describes current commands, supported providers, ownership boundaries, and limitations |

## OpenCode Go support

OpenCode Go is not a single OpenAI-compatible endpoint: its catalog is split across chat completions, Anthropic-style messages, and OpenAI Responses. Supporting it by changing `base_url` alone would work for only part of the catalog, so the client routes per model family (overridable with `api_format`) and implements all three request/response shapes with the same validation and size limits as the existing path.

Specifically:

- Model-to-format routing matches the published endpoint table; explicit `api_format` covers new or self-hosted models.
- The Messages path uses `x-api-key` + `anthropic-version` and deliberately sends no bearer token; the chat and Responses paths use bearer auth.
- `x-opencode-session` is generated once per client (one command invocation) and reused so routing/prompt caching stays stable; `rand.Text()` avoids guessable IDs. A non-generic `User-Agent` is sent on all requests.
- Responses requests set `store: false` rather than relying on account-level retention settings.
- Provider switching (`COMMIT_PROVIDER`) drops the previously stored key, endpoint, format, and model, and the OpenAI endpoint is rejected for `opencode-go` credentials (and vice versa) so a stale key cannot be sent to the wrong host.
- Reasoning/thinking blocks are ignored rather than surfaced, and only terminal `stop`/`end_turn`/`completed` states with text output are accepted; truncation and refusals fail loudly.
- Documentation notes that model data-retention and training terms differ across the Go catalog, because diffs may contain proprietary code.

## Follow-up fixes after the first pass

Reviewing the new implementation again surfaced issues introduced by the redesign itself:

| Issue | Fix |
| --- | --- |
| The configured 60s Git timeout was applied to `git push`, so a healthy large push could be killed mid-transfer | Pushes get a separate 15-minute bound; every other Git command keeps the configured timeout, and Ctrl-C still cancels immediately |
| Explicitly empty `--issue` was accepted | It now fails with an actionable validation error. **Correction:** the earlier claim that this dropped closing references was wrong: parsing blank input returns `nil`, which already preserved existing links. |
| Rejecting every Unicode format character also rejected ZWNJ/ZWJ, breaking legitimate Persian, Arabic, Indic, and emoji text | Only ZWJ/ZWNJ are permitted; bidi controls, zero-width space, BOM, soft hyphen, interlinear annotation, and all C0/C1 controls (ANSI escapes) remain rejected |
| HTTPS remotes with embedded credentials were refused, breaking some CI checkouts | Only `owner/name` is passed to the GitHub API adapter. Raw URLs are not printed. Comparison ignores HTTPS userinfo only; SSH usernames are preserved and URL queries/fragments are rejected. |
| CI ran tests and vet but no vulnerability scan | Added a `govulncheck` job |
| GoReleaser `formats` requires v2.6+ and this was undocumented | Documented the minimum version |

## Review fixes

- **Branch identity:** hash/diff equality alone missed switching to another branch at the same commit. Commit plans now record and recheck the full symbolic HEAD target. Real-Git regression tests cover branch switches, renames, attaching/detaching, and unborn branches; unchanged detached and initial commits remain supported.
- **Process-tree cancellation:** cancelling only the direct Git process left hooks running. Unix commands now use a dedicated process group terminated on cancellation. Windows commands start suspended, join a non-breakaway Job Object, and resume only after assignment; cancellation terminates the job. Containment failures abort startup rather than falling back to an uncontained process. Regression tests use a descendant-owned loopback connection to verify termination, plus a real Unix pre-commit hook.
- **Destination comparison:** removing every username and query string was too permissive. Only HTTPS userinfo is ignored; SSH usernames are preserved, queries/fragments are rejected, and one-URL-per-line parsing supports local paths with spaces.
- **Terminal prompts:** a dedicated process group is a background job, and reading the terminal stopped it with SIGTTIN until the timeout killed it. SSH passphrases/host-key confirmation, HTTPS credential prompts, interactive hooks, and signing silently hung for 60s (15 minutes for push). Interactive `ls-remote`, `push`, and `commit` now stay in the foreground group; other commands and non-interactive runs use their own session, so a prompt fails fast. A helper-process test checks group/session placement; the hang was reproduced and verified fixed under a pseudo-terminal.
- **Git diagnostics:** Git stderr was discarded, so failures read `git remote failed: exit status 2`. A bounded, credential-redacted, control-stripped tail is now included in errors, push output is streamed, and "nothing staged"/"no branch changes" have specific messages. Timeouts name the command, and push errors no longer say `git -c failed`.
- **Commit timeout:** `git commit` shared the 60s per-command timeout, killing slow hooks or signing mid-way. Commits now use the 15-minute long-command bound.
- **Repository case:** PR URLs were compared case-sensitively against the remote path, so `MyUser/Repo` remotes created a PR and then reported failure (and later runs failed). The canonical `full_name` is adopted from the repository response and URL validation is case-insensitive and exact (`/owner/name/pull/N`).
- **Issue extraction:** a case-insensitive pattern treated prose such as "Fixes utf-8 decoding" as issue `UTF-8`. `#N` references still match anywhere; team keys must be uppercase on their own line.
- **Unknown commands:** a root `NoArgs` validator made `commit comit` print help and exit 0. Removing it lets Cobra report `unknown command` with suggestions and exit 1.
- **Check before sending/prompting:** workflows are split into read-only inspection and generation. "Generating…" is printed, and the PR issue prompt shown, only after local and GitHub checks pass, so a user never types issues or sees a send notice for a run that fails on a missing branch, base ref, or staged change.
- **PR JSON robustness:** a reply wrapped in one code fence is unwrapped before strict validation, and OpenAI's own API is asked for `json_object` output. Other compatible servers are not sent `response_format`, since some reject it.
- **Interrupts:** Ctrl-C/SIGTERM printed `Error: context canceled` and exited 1. They now print `Interrupted.`/`Terminated.` and exit 130/143, preserving extra context (for example after a push), and a second Ctrl-C exits immediately.
- **Silent upstream change:** every push used `-u`, carried over from the initial implementation without a stated reason (it is the common first-push idiom). It silently replaced deliberate upstreams, such as `origin/main` on a branch created with `--track` or from `origin/main` (the default `branch.autoSetupMerge` behavior), changing what `git pull` and `git status` compare against. Now `--set-upstream` is passed only when the branch has no upstream, the plan discloses it (or notes that an existing upstream is kept), and a changed upstream between review and apply aborts.
- **Test isolation:** CLI configuration tests now unset all provider/config environment variables with cleanup restoring their original values and presence. A private default config directory prevents reading the developer's configuration. Tests also run under deliberately conflicting dummy environment values.

## Compatibility

Preserved command names: `commit`, `pr`, `init`, `version`, `update`.
Preserved flags: `--auto-accept/-y`, `--issue/-i`, `--draft/-d`, `--check/-c`, `--force/-f`.
The executable is now `commit`; `commit commit` is intentional.

Additions: `--config`, `--dry-run`, `--no-browser`, root `--version`, shell completion.

Deliberate safety changes: blank mutation confirmations decline; redirected input requires an explicit mode; invalid issues/config fail; init does not overwrite existing files; no pushes until approval; PR content is shown before mutation; no silent diff truncation or background update calls. Config uses the new app-specific path and does not read the previous app's config automatically.

## Boundaries and remaining limitations

- The module identity is `github.com/myusuf3/commit`. Update source configuration remains explicit; unconfigured update returns an actionable error rather than contacting a publisher automatically.
- CLI compatibility means names/flags and workflows, not byte-for-byte terminal output.
- PRs target the default branch on `origin` at github.com. Users fetch their base refs explicitly. Fork PRs, alternative remotes, custom bases, and Enterprise need future work.
- Existing PR title/body regeneration replaces human edits after review. Only recognized issue-closing lines are retained automatically; `--draft` does not convert existing PRs.
- Model output can be wrong or contain sensitive material despite structural validation. The user owns review and provider trust; there is no automatic secret scrubber.
- Process-tree cancellation is not a sandbox or rollback mechanism. Deliberately detached Unix helpers may escape their process group, and side effects already completed cannot be undone. Windows runtime behavior must be validated in the Windows CI job; local cross-compilation alone is not proof.
- Revalidation is not an atomic Git transaction. Concurrent changes after checks and hook modifications remain possible. Push and GitHub update are not a transaction either.
- No automatic retries for mutating requests: a lost response can mean the remote operation succeeded. Inspect the repository before retrying ambiguous failures.
- A checksum is not a signature. Release publisher trust is explicit; private asset downloads, signatures, and Windows self-replacement are not implemented.
- Cross-compilation and local tests do not establish live-provider behavior or hosted CI success. External actions should be tested only with a chosen repository and credentials.
