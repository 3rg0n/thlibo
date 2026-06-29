# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Documentation

- **README "install footprint" section** (#51): a table of every path
  `thlibo install` touches, the SHA-stamp/`.new` edit-preservation, and
  an explicit note that install does *not* modify
  `skipDangerousModePermissionPrompt`, `skipWebFetchPreflight`, or any
  other `settings.json` safety key (correcting a misconception from the
  #47 audit). Plus a "two behaviors worth knowing" subsection surfacing
  the PreToolUse auto-allow (`permissionDecision: "allow"`) and the
  persistent hook — both already named in `THREAT_MODEL.md` (MA-2, MA-6)
  but previously absent from user-facing docs. Also corrected a stale
  claim that inferd's Windows install needs admin (zero-touch since
  v0.7.5).

## [0.7.5] - 2026-06-29

### Fixed

- **Inferd download silently truncated → "not a valid zip" on every
  fresh install.** `download`'s 200 MiB cap (a stale guard from the
  llamafile-less era) clipped inferd's now-~633 MB release bundle (it
  ships the ggml/CUDA/Metal backends), and `io.LimitReader` truncates
  without error, so extraction failed on a corrupt archive. Raised the
  cap to 2 GiB and made an over-cap response a loud error instead of a
  silent truncation. Caught during v0.7.5-rc.1 fresh-install validation.
- **Windows install was not zero-touch.** `runInferdInstaller`'s Windows
  branch assumed inferd's `install.ps1` needed admin (registers a
  service) and punted to a manual elevated step. The current `install.ps1`
  is a no-admin per-user install (Startup-folder shortcut, same posture
  as the macOS LaunchAgent / Linux systemd-user). thlibo now runs it
  directly, so a fresh Windows install starts the daemon and registers
  login autostart with no human step — matching macOS/Linux.
- **Fresh install left the inferd daemon dead on macOS/Linux** (#47).
  `thlibo install` copied only the `inferd-daemon` binary to
  `~/.local/bin`, not the `backends/` ggml libs that must sit beside it.
  inferd's `install-launchagent.sh` then aborted (`exit 1`, missing
  `libllama.dylib`) *before* installing the LaunchAgent or starting the
  daemon — so a "complete" install produced no autostart and no running
  daemon. Now `runInferdInstaller` copies the whole `backends/` dir next
  to the binary first (macOS + Linux + the Windows staged dir), so the
  launchagent/systemd path completes and the daemon starts.
- **thlibo installed a *pre-release* inferd.** inferd tags its RCs
  (`v0.5.1-rc.1`) without GitHub's prerelease flag, so `/releases/latest`
  returned the RC and thlibo's flag-based guard never tripped. Tag
  resolution now scans the releases list and skips any hyphen-suffixed
  (pre-release) tag, resolving the latest *stable* inferd (e.g. v0.5.0).
  `--inferd-version` still pins an explicit RC when wanted.
- **Install now verifies the daemon actually came up.** After a fresh
  install, thlibo probes the admin socket (≤15s) and reports "installed
  and started (daemon reachable)" vs. "installed; daemon not reachable
  yet" + a hint — instead of a blanket "complete" that hid a dead
  daemon.

### Added

- **`go-test-filter` processor** — compresses `go test -v` (and
  `go test -json`) output: keeps every failing test's `--- FAIL` line +
  its detail (file:line, got/want), build errors, and panics verbatim,
  plus the per-package tally; drops the `=== RUN` / `--- PASS` noise for
  passing tests. Monotonic guarantee (passthrough if not a strict byte
  win). Closes the `go test` interception gap from the #41 macOS survey
  (#42).
- **Subcommand-aware command matching** (`command_prefixes` in a
  processor descriptor + `Registry.MatchCommandLine`). The rewrite hook
  can now wrap a specific subcommand — `go test` routes to
  `go-test-filter` while `go build` / `go run` / `go vet` are left
  unwrapped. (Previously `commands:` matched argv[0] exactly, which
  couldn't distinguish `go` verbs.)

## [0.7.4] - 2026-06-26

### Added

- **Scanned-PDF OCR via Gemma vision** ([ADR 0009](docs/adr/0009-pdf-image-ocr-via-gemma-vision.md),
  supersedes ADR 0007's scanned-PDF deferral). Image-only PDFs — which
  previously returned only `[scanned page N — OCR not yet supported]`
  placeholders and a low-value passthrough (#31) — now return real
  transcription. When `casefile.Create` detects the low-value sentinel
  and a vision-capable inferd is reachable, the Go side rasterizes each
  page (via the `pdf-to-md` processor's new `--render-page` / `--page-count`
  modes), decodes to raw RGB, and sends it through the inferd v2 image
  attachment path (`internal/inferd` BLOB frames; new `internal/pdfocr`)
  with an OCR prompt. Capability-gated and fail-open per page (ADR 0006):
  if vision is unavailable or a page errors, the prior placeholder
  behaviour is preserved. Page-capped (default 25) to bound vision cost.
  - `internal/inferd` gains the image attachment / BLOB-frame path
    (protocol-v2.md §3.5/§3.7): metadata-only attachment JSON + raw RGB
    in a `0x02` frame, never base64 (inferd ADR 0016).

### Changed

- **Migrated to the inferd v0.4+ unified IPC wire; thlibo now owns its
  wire codec.** thlibo no longer depends on inferd's `clients/go`
  reference client — `internal/inferdcli` (which spoke the removed
  protocol-v1 NDJSON surface and dialed the now-defunct
  `inferd-infer` socket) is deleted and replaced by `internal/inferd`,
  a thin codec implemented directly against inferd's `protocol-v2.md`:
  length-prefixed `0x01`/`0x02` framing, in-band `wire_version`, the
  unified generation socket (`\\.\pipe\inferd` / `inferd.sock`), and a
  stream→string collapse. Fail-open (ADR 0006) stays at the middleware
  boundary. A pre-v0.4 daemon is no longer supported (the v1 socket was
  removed daemon-side); thlibo works against inferd v0.4.0 and v0.5.0.
  - **No inbound TCP transport.** inferd v0.5 (ADR 0022) binds no
    network listener; the client's TCP path is removed accordingly —
    UDS (Unix) / named pipe (Windows) only.
  - **Router uses `response_format` JSON-Schema** (inferd v0.5) to
    constrain routing output to `{"processors":[...]}`, restoring the
    hard tool-call guarantee daemon-side (GBNF via the gateway) that the
    removed v1 GBNF grammar field provided. Unparseable output or an
    unknown processor name still falls back to passthrough (B8c).

### Added

- **Daily log rotation + 7-day FIFO retention** in `internal/logx`.
  Replaces the previous size-based `.old` cascade. Live file is
  `<component>.ndjson`; archives are `<component>-YYYY-MM-DD.ndjson`.
  Retention is configurable via `THLIBO_LOG_RETAIN_DAYS` (default 7).
  The first write of a new day rotates the previous live file under
  the closing day's date and sweeps any archives older than the
  retention window. Live records and unrelated files in the directory
  are never touched by the sweep.

### Changed

- **`thlibo case` exits 6 (`ExitLowValue`) when compressed output is
  placeholder-only.** The case directory is still written for
  forensics, but stdout is empty so the Read PreToolUse hook treats
  it as no-match and lets Claude Code's native PDF reader handle
  the original. Unblocks the scanned-PDF flow that previously caused
  Claude to fall back to manual `pip install pypdf` + PNG rendering
  (issue [#31](https://github.com/3rg0n/thlibo/issues/31)).
  - `pdf-to-md` emits `<!-- thlibo-pdf-low-value: ... -->` when no
    page yields extractable text.
  - `casefile.Meta.LowValue` carries the flag forward to `meta.json`
    and `summary.md`.
  - The activity log records `low_value=true/false` per case.

### Fixed

- **`pdf-to-md` crashed on Windows for text PDFs containing non-ANSI
  characters.** Python on Windows defaults stdout to the legacy
  cp1252 code page, so a PDF with `→`, em-dashes, or smart quotes
  raised `UnicodeEncodeError`, the processor exited non-zero, and the
  pipeline fell back to emitting the raw PDF bytes (0% reduction). The
  processor now forces `sys.stdout` to UTF-8 alongside the existing LF
  reconfigure. Output is byte-identical across Windows, WSL, and
  Linux. (Linux/macOS were unaffected — their stdout already defaults
  to UTF-8.)

## [0.7.3] - 2026-05-26

### Added

- **Headless detection** (`internal/update.IsHeadless`) — explicit
  `THLIBO_HEADLESS=1/0` override; otherwise headless if any of
  `CI`, `GITHUB_ACTIONS`, `GITLAB_CI`, `BUILDKITE`, `CIRCLECI`,
  `JENKINS_URL`, `CLAUDECODE`, `CLAUDE_CODE_SESSION_ID`, `CODEX`,
  or `CODEX_SESSION_ID` is set, with a stderr-TTY fallback. The
  agent markers (`CLAUDECODE` / `CODEX*`) are load-bearing for
  thlibo's primary use case — without them the headless notice
  would never fire when an AI assistant pipes tool output through
  the binary. TTY detection now uses `golang.org/x/term`, which
  works uniformly on Linux (TCGETS), macOS/BSD (TIOCGETA), and
  Windows (`GetConsoleMode`).
- **Headless update notice** — when running non-interactively (hooks,
  pipes, CI), a `[thlibo] new update available, run: thlibo upgrade`
  line is prepended to stdout once per new release tag. The literal
  is a compile-time constant so no release-server content reaches
  the AI client.
- **`thlibo upgrade` subcommand** — shells out to the signed install
  script (`bash -c "curl -fsSL … | bash"` on Unix; PowerShell
  `Invoke-Expression` on Windows). Accepts `--version v0.X.Y` to
  pin a specific tag; supports `THLIBO_VERSION` env passthrough.
- **Desktop toast notifications** — macOS via `osascript display
  notification`, Linux via `notify-send --app-name=thlibo
  --icon=dialog-information` (libnotify; gracefully no-ops if
  `notify-send` is missing or DBus is unavailable, with a 2 s
  timeout to guard against a stuck notification daemon). Title and
  body are compile-time constants (prompt-injection guard). Windows
  toast support is deferred — needs an AUMID and registered
  shortcut.
- **Per-channel notification de-dup** — `NotifiedTag` for interactive
  stderr banner, `HeadlessNotifiedTag` for stdout injection; each
  channel fires at most once per new tag.
- **Headless sync in main** — when `IsHeadless()` is true the runner
  goroutine is awaited before the subcommand runs, guaranteeing the
  notice line appears first in piped output.

### Changed

- Interactive banner now says `run: thlibo upgrade` instead of the
  raw curl one-liner (reduces noise; upgrade subcommand handles it).

## [0.7.2] - 2026-05-26

### Added

- **`trivy-filter` processor** — distills Trivy's default box-drawing
  vulnerability tables into one TSV line per CVE. Parses the
  `Library / Vulnerability / Severity / Status / Installed Version /
  Fixed Version / Title` columns, merges multi-row title wraps,
  carries the library name across blank-cell continuation rows,
  drops the URL suffix appended to each title, and emits findings
  sorted by severity (criticals first). Output schema:
  `severity-letter \t lib@installed \t CVE \t fixed \t title`.
  On the demo `trivy fs requirements.txt` fixture (61 CVEs, 5 libs):
  54.6 KB → 7.2 KB, **86.8 % savings**.
  Match-driven on the `Library | Vulnerability | Severity` header
  row or the `Total: N (UNKNOWN: …, CRITICAL: …)` banner. Reads
  stdin as UTF-8 (Windows code-page fix for the box-drawing chars).
  10-case unit test covers single + multi-finding tables, lib
  carry-forward across separators, URL stripping, severity sort,
  monotonic-guarantee passthrough, and tiny / empty inputs.

### Changed

- **`lint-filter` rewrite — verbose-distill, terse-passthrough,
  monotonic guarantee.** v0.7.1 only meaningfully compressed gcc
  verbose; everything else either grew the input or left the
  format unparsed. v0.7.2 splits the input into two paths:
  - **Verbose-shape detection.** A new `_VERBOSE_HINT` regex
    catches rustc / clippy / ruff multi-line block openers,
    `--> file:line:col` location continuations, gcc / rustc
    source-snippet lines (`   N | source`), and eslint stylish
    indented `line:col level msg rule` rows.
  - **Verbose path** (`_parse_verbose`) walks the buffer
    statefully, collapsing each multi-line block into one
    finding. Captures the `= note: \`#[warn(rule_name)]\``
    rule on rustc / clippy and the `[*] CODE msg` opener on
    ruff verbose. `= help: …` (or `help: …`) suggestion lines
    become a `-`-prefixed continuation row immediately under
    the finding so the AI sees the fix-suggestion verbatim.
  - **Terse path** parses single-line shapes (eslint compact /
    unix, golangci, ruff concise, mypy, shellcheck, rubocop,
    stylelint, tsc) and re-emits them — never overrides the
    model's flag choice when the linter already chose terse.
  - **Output schema is TSV**: `severity-letter \t loc \t rule
    \t msg`, four columns. Severity letters: E / W / I / N /
    S / C / R. Replaces the old fixed-width columnar formatter.
  - **Monotonic guarantee.** End of `compress()` compares
    distilled UTF-8 byte count against the input; if distillation
    didn't shrink the bytes, the input is returned verbatim.
    No code path can grow what the AI client receives.
  - **`MAX_PER_RULE` defaults to 0** (no cap) — was 5 in v0.7.1.
    The cap now exists only as an opt-in knob via
    `LINT_MAX_PER_RULE` for callers who want it.

  End-to-end against 13 captured outputs (rustc, clippy verbose +
  short, gcc verbose, ruff verbose + concise, mypy, golangci,
  staticcheck, go vet, tsc, eslint stylish + compact + unix):
  **77.0 % aggregate savings** (66.6 KB → 15.3 KB), with no
  individual format showing negative savings. Verbose-default
  formats compress 43–56 %; terse-default formats passthrough
  cleanly at 0–10 % (CRLF→LF normalisation only). 29-case unit
  test covers every format plus the monotonic-guarantee fallback.

### Fixed

- **`lint-filter` negative-savings on terse formats** — v0.7.1's
  TSV reformatter could grow short inputs (staticcheck 74 → 161 B,
  golangci 279 → 395 B, mypy 137 → 211 B) because column letters
  and rule grouping added overhead the linter's own format
  already lacked. The monotonic guarantee in the rewrite
  eliminates this: if the rewrite isn't a strict win, the input
  is returned unchanged.

- **`lint-filter` format gaps** — clippy / ruff multi-line blocks,
  tsc parens-format, and eslint stylish file-header rows were not
  matched by any v0.7.1 parser, so those buffers passed through
  verbatim with zero compression. v0.7.2 parses all three.

## [0.7.1] - 2026-05-26

### Added

- **`lint-filter` processor** — compresses output from the major
  linting / static-analysis tools: clang, gcc, clippy (rustc-style),
  eslint (default + compact + unix formats), golangci-lint,
  shellcheck, flake8, ruff, mypy, rubocop, stylelint. Parses each
  finding into (severity, file:line:col, rule-id, message), groups
  by rule, dedupes identical findings across files, sorts errors-
  before-warnings, and caps to N findings per rule (default 5,
  override via `LINT_MAX_PER_RULE`). Drops surrounding context lines
  (rustc carets, gcc source excerpts, eslint per-file blank lines,
  `= help:` / `= note:` prose) and ANSI colour codes. Tail line
  records the original total per rule so the AI can see what was
  elided. Match-driven (auto-fires on lint-shaped output); no
  `commands` whitelist because lint output flows from many wrappers
  (`npx eslint`, `python -m flake8`, `cargo clippy`, etc.).
  21-case unit test covers each format + cap behavior + ANSI strip
  + passthrough on non-lint inputs.

## [0.7.0] - 2026-05-26

### Added

- **`cordon-filter` processor** — semantic anomaly surfacer for
  unstructured / weakly-structured logs and long-form text.
  Windows the input into N-line chunks, embeds each window via
  inferd's embed socket (EmbeddingGemma 300M, MRL-256), scores
  each window by k-NN density in embedding space, and emits the
  top-percentile windows as the same `signature_groups` shape
  that the `compress` prompt produces. Fail-open contract: if
  inferd is unreachable, numpy is missing, or anything else
  goes sideways, the input passes through verbatim.
  `CORDON_MAX_WINDOWS` caps the O(n²) pairwise-distance step on
  large inputs (uniform sampling). E2e on real fixtures:
  543 MB traefik 7-day log → 169 KB / 272 anomaly groups; 551 MB
  application log → 254 KB / 505 groups; 17-page PDF → 49 KB /
  287 groups. See [ADR 0008](docs/adr/0008-numpy-as-processor-dep.md)
  for the numpy soft-import pattern this introduces. Tested
  against [inferd](https://github.com/3rg0n/inferd) v0.2.4.

### Fixed

- **`cordon-filter` signature collapse on structured logs**:
  the original `_signature()` truncated the tokenised line at 40
  chars, before any discriminating field appeared in JSON / Loki
  / OTel records. On real traefik access logs every record
  collapsed to one signature, which would defeat the surfacer's
  purpose on production log fixtures. Rewrote `_signature()`
  with two paths: (1) detect JSON, lift discriminating keys
  (`level`, `RequestMethod`, status-class like `5xx` / `4xx`,
  `RequestPath` stem with numeric / hex / UUID segments
  tokenised, `caller`, `msg` stem) into a stable kebab-case
  composite; (2) plain text falls through to the token-replace
  path with the prefix cap raised 40 → 80 chars. `_level()` now
  reads `level` / `detected_level` / `severity` directly out of
  structured records. 14-case unit test pins the regression.

## [0.6.2] - 2026-05-21

Follow-up to v0.6.1's version gate. mac claude's macOS smoke (#24)
caught a hole: the gate only checked the binary on disk. If an
older inferd was already running, `probeInferdAdmin` returned
reachable, the orchestrator short-circuited to `UsedExisting`,
and no version comparison ever happened — so a user who had
inferd 0.1.13 running before installing thlibo v0.6.1 would
silently keep getting mock inference.

### Fixed

- **Running-daemon version check** (#26 / closes #25): when
  the admin socket is reachable, resolve the daemon version
  (from the admin status frame if present, falling back to
  shelling `inferd-daemon --version` for pre-v0.1.14 daemons
  that don't include a version field), compare against
  `MinInferdVersion`, and if too old: stop the running daemon
  via the platform service manager (`systemctl --user stop` /
  `launchctl bootout` / `sc.exe stop`) and fall through to the
  fresh-install branch. New `stopInferd()` mirrors the shape
  of `startInstalledInferd`. Best-effort silent stop — the
  installer's `enable --now` / `bootstrap` / re-create
  overwrites the unit / plist / service either way.
  Verified by mac claude during #24 follow-up: thlibo install
  with inferd 0.1.13 running emits "stopping for upgrade",
  runs the fresh-install path, lands on v0.1.14 with
  `--backend llamacpp` in the plist.

## [0.6.1] - 2026-05-21

Hotfix release. v0.6.0's release pipeline + installer scripts
shipped three regressions in code that only runs at install time;
this release lands all three fixes plus a CI guard so they cannot
recur, and a version gate that detects stale inferds (the kind of
bug v0.6.0's lack of install-path tests would have caught).

### Fixed

- **`scripts/install.sh` / `scripts/install.ps1`**: v0.6.0
  shipped scripts that still copied `thlibod[.exe]` (gone in
  v0.6.0) and called `thlibo install --pull-engine --pull-model`
  (flags removed when inferd became the sidecar). Both rewritten
  to extract just `thlibo[.exe]` and invoke `thlibo install`.
  This is the change that unblocks `curl -fsSL ... | bash` and
  `irm ... | iex` from main.
- **`.github/workflows/release.yml`**: dropped the leftover
  `thlibod` build step + llamafile-engine bundle download +
  `pinnedGemma4E4BQ4KM` ldflag. Bundles now contain only
  `bin/thlibo`. Switched cosign signing from
  `--output-signature` / `--output-certificate` (deprecated in
  cosign v3) to `--bundle <file>.cosign-bundle` (sig + cert +
  Rekor entry in one file). Verifying signatures now uses
  `cosign verify-blob --bundle <file>.cosign-bundle <file>`.
- **Sidecar inferd version gate**: `thlibo install` now refuses
  to delegate to an inferd binary older than `MinInferdVersion =
  v0.1.14`. Older builds carry two bugs: WSL ENOSPC during
  llamacpp init (inferd commit 1fe99d4 / inferd #6, fixed in
  v0.1.13), and a macOS launchagent that registered a mock
  daemon because the install script never wrote `--backend
  llamacpp` / `--model-path` (inferd #8 / #9, fixed in v0.1.14).
  Stale binaries trigger the fresh-install branch which runs
  inferd's installer; both the binary and the install script
  get refreshed. Version comparator tolerates leading-`v`,
  `-rc` / `+build` trailers, four-component versions, and
  empty input. 13-case unit test plus a floor-pinning test that
  fails fast on a careless edit. Verified end-to-end on a WSL
  host: downgraded inferd to v0.1.11, ran `thlibo install`,
  observed `found inferd 0.1.11 ... but it's older than the
  minimum supported v0.1.14; upgrading` followed by
  `inferd v0.1.14 installed via inferd's installer`. Closes #21.

### Added

- **`verify-install` CI matrix job** in `release.yml`. Between
  `build` and `release`, downloads the just-built archive on
  ubuntu-latest + windows-latest, runs
  `scripts/install.{sh,ps1}` against it (via the new
  `THLIBO_LOCAL_ARCHIVE` env var), and asserts the resulting
  binary reports the tag. The `release` job now needs `[build,
  verify-install]` — a broken installer cannot reach
  `gh release create`. Closes #20. Without this, the next
  v0.6.x would have shipped the same way v0.6.0 did.
- **`THLIBO_LOCAL_ARCHIVE` env var** on both installer scripts.
  When set, skip the GitHub Releases download + SHA-256 verify
  and use the supplied file. CI / release-verification only;
  the user-facing flow is unchanged.

## [0.6.0] - 2026-05-19

### Added (PDF processor; shipped in the v0.6.0 squash)

- New `pdf-to-md` script processor. Converts a PDF (born-digital,
  text-based) into GitHub-flavored markdown: TOC reconstructed
  from the PDF outline, per-page rendering, tables emitted as
  GHFM tables, numbered section headings promoted to `##`/`###`
  via a tightened heuristic (numeric prefix + uppercase title +
  no internal periods + length cap, to avoid falsely promoting
  numbered list items or table cells).
- Image surfacing is placeholder-only in v0.7
  (`[image: page N — vision not yet supported]`). v0.8 will
  collapse OCR (scanned pages) and chart-description (image-only
  pages) into a single feature: render the page as an image and
  send it to inferd's multimodal Gemma 4 with a transcription or
  chart-description prompt — both paths use the same wire
  endpoint, only the prompt differs. Gated on inferd exposing
  image payloads over its NDJSON wire (in flight).
- Triggered by the fast-path regex `^%PDF-`. Verified end-to-end
  on a 29-page Cisco PRD (327 KB → 70 KB in 1.2 s, 6 tables
  cleanly rendered as GHFM tables, 11 top-level sections + 10
  subsections promoted to markdown headings), a 19-page MIME-info
  spec (146 KB → 35 KB), a USENIX academic paper (351 KB →
  7.5 KB), and a generated table fixture.
- Adds `pypdf` (BSD-3) + `pdfplumber` (MIT) to thlibo's Python
  processor dependencies; install via
  `pip install -r ~/.thlibo/processors/pdf-to-md/requirements.txt`
  after `thlibo install` mirrors the processor. See
  [ADR 0007](docs/adr/0007-pdf-to-markdown.md) for the full
  library shootout — thlibo's dep tree is permissively-licensed
  end-to-end (MIT / BSD / Apache); copyleft-licensed tools in
  this space (pymupdf, marker) are excluded entirely, including
  from any opt-in user-recipe documentation.

---

The inferd extraction is real. Thlibo is now pure middleware: hooks,
processors, settings.json merge, registry, router. Inference moved
to the separate [inferd](https://github.com/3rg0n/inferd) project,
which thlibo dials over its frozen NDJSON protocol-v1 wire.

### Removed

- `cmd/thlibod/` — embedded inference daemon. Inferd owns this now.
- `internal/daemon/` — engine supervisor, llamafile spawner, lock,
  server-side peer-cred, lifecycle.
- `internal/queue/` — single-active admission queue (inferd manages
  its own).
- `internal/ipc/` — NDJSON wire types, endpoint, peer-cred client.
  Replaced by import of `github.com/3rg0n/inferd/clients/go`.
- `internal/install/{model,engine}.go` — model GGUF + llamafile
  binary download. Inferd handles model bootstrap.
- `cmd/thlibo/pullcmd/` — `thlibo pull` removed. Run `inferd pull`
  instead. The `pull` subcommand now prints a deprecation message
  pointing at inferd.
- Install flags `--pull-engine`, `--pull-model`, `--allow-unpinned`,
  `--engine-path`, `--daemon-path`, `--skip-autostart` — gone with
  the daemon they configured. Inferd's installer registers inferd's
  own autostart entry.

### Added

- `internal/inferdcli/` — thin wrapper around the inferd Go client
  exposing the connection-per-call `Post(ctx, Request) -> (string, error)`
  shape thlibo's hot path uses. Implements **passive readiness**
  per ADR 0006: connect-and-retry against the inference socket;
  connect failure surfaces as `ErrBackendNotReady` and the caller
  passes the original bytes through. Six unit tests cover stream
  collapse, done-frame fallback, error frame surfacing, refused-
  connect detection, TCP-shape detection, and transient-error
  classification across Linux / macOS / Windows error wording.

### Changed

- `internal/router/`, `internal/middleware/`, `cmd/thlibo/{execcmd,
  compresscmd,shorthandcmd}` — all rewired from the old
  `internal/router.DaemonClient` to `internal/inferdcli.Client`.
  The Gemma-native tool-call routing logic, GBNF grammar building,
  unknown-name fallback, and prompt sanitization all carried over
  unchanged; only the wire transport swapped.
- Default endpoint paths now point at inferd's locations:
  `$XDG_RUNTIME_DIR/inferd/infer.sock` (Linux),
  `$TMPDIR/inferd/infer.sock` (macOS), `\\.\pipe\inferd-infer`
  (Windows). Match the protocol-v1 spec inferd publishes.
- `thlibo install` is now middleware-only: hooks, processors,
  settings.json merge, optional Codex hook. The autostart
  registration moved to inferd's installer.

### Migration from v0.5.x

`thlibo install` now auto-detects v0.5.x installs and migrates
them in place. Idempotent: safe on fresh installs (no-op) and
on already-migrated hosts (also no-op). On detection it:

- Stops + disables the v0.5 daemon autostart unit:
  - Linux: `systemctl --user stop/disable cisco.thlibo.daemon.service`,
    removes the unit file
  - macOS: `launchctl bootout` + removes `cisco.thlibo.daemon.plist`
  - Windows: removes `cisco.thlibo.daemon.cmd` from `Startup\`
- Deletes the dead binaries: `thlibod` and `thlibo-engine` (the
  ~878 MB APE llamafile)
- **Moves the model** from `~/.thlibo/models/<name>.gguf` to the
  shared model store at the platform's standard data dir:
  - Linux: `${XDG_DATA_HOME:-$HOME/.local/share}/models/`
  - macOS: `~/Library/Application Support/models/`
  - Windows: `%LOCALAPPDATA%\models\`

  No redownload required. Atomic `rename(2)` on same-filesystem
  moves; falls back to copy + delete for cross-filesystem.
- Cleans up `~/.thlibo/{models,logs,run}/` (daemon-only state)
- Preserves `~/.thlibo/{processors,hooks,config.yaml,state}/` —
  still load-bearing in v0.6

Verified end-to-end on Ubuntu 26.04 / WSL2 against a real v0.5.4
install: 5.1 GB GGUF moved cleanly, all daemon artefacts gone,
processors / hooks / settings.json untouched. Second run shows
no migration block (idempotent).

Existing `~/.claude/settings.json` hook entries keep working
through the upgrade — re-running `thlibo install` refreshes them
to current SHA-stamped versions.

The shared model store at `~/.local/share/models/` (and
equivalents) is where inferd should also look for the GGUF —
follows the cross-tool model-store convention drafted in
`.plan/spec.issue.md`.

## [0.5.4] - 2026-05-18

### Fixed

- Linux: `thlibod` failed to start under systemd because it tried to
  `mkdir /run/thlibo`, which is not writable from a per-user systemd
  unit. Replaced the hard-coded `/run/thlibo` paths with a
  `linuxRuntimeDir()` helper that prefers `$XDG_RUNTIME_DIR` (= the
  systemd-managed `/run/user/<uid>`) and falls back to
  `$HOME/.thlibo/run` for sessions without logind. The systemd unit
  now also declares `RuntimeDirectory=thlibo` +
  `RuntimeDirectoryMode=0700`, so systemd auto-creates the path with
  the right perms before ExecStart, working alongside
  `ProtectSystem=strict`. Confirmed end-to-end on Ubuntu 26.04 /
  WSL2: daemon binds the lock file at
  `/run/user/1000/thlibo/thlibod.lock` and the only remaining
  failure mode is the (unrelated) WSL APE-engine issue documented
  below. Sites changed: `internal/ipc/endpoint.go` (default infer +
  admin socket), `cmd/thlibod/main.go` (default lock path),
  `internal/install/autostart_linux.go` (systemd unit template).
- Daemon no longer crash-loops when the LaunchAgent / systemd unit
  fires before `thlibo install --pull-engine --pull-model` finishes
  downloading. Two complementary fixes:
  1. `thlibo install` now registers the autostart entry **after**
     all downloads complete instead of before, so the first
     supervised start always has engine and model on disk.
  2. `thlibod` adds a preflight wait loop: if either file is
     missing at startup it sleeps 30 s and retries for up to 5
     minutes, logging a `waiting_for_assets` entry. Hooks pass
     through unchanged during the wait so the user sees no
     interruption. After 5 minutes it proceeds and lets
     `daemon.Start` surface the real error.

### Added

- WSL detection in `thlibo install`: when running under WSL with
  `/proc/sys/fs/binfmt_misc/WSLInterop` active, the installer now
  prints a one-time advisory before exiting. The llamafile engine
  is a polyglot APE/Cosmopolitan binary (MZ + ELF) and WSL's
  binfmt_misc handler intercepts MZ-magic executables as Windows
  binaries — so the daemon dies with `error: APE is running on
  WIN32 inside WSL`. The advisory shows the documented escape
  hatches: `sudo sh -c 'echo -1 > /proc/sys/fs/binfmt_misc/WSLInterop'`
  (per boot) or `[interop] enabled = false` in `/etc/wsl.conf`
  (permanent). Hint also fires under `--dry-run`.

### Known limitations

- llamafile's `--host <unix-socket>` mode does not bind correctly
  on the v0.10.1 engine we ship for Linux: the engine logs
  `start: couldn't bind HTTP server socket, hostname: …` and
  exits before health-check. The lock + RuntimeDirectory work
  above is independent of this bug — the daemon now reaches the
  engine-spawn step cleanly. The engine UDS-mode bug goes away in
  v0.6.0 when inferd takes over inference (ADR 0005); not worth
  patching the soon-to-be-deleted llamafile spawn path.

## [0.5.3] - 2026-05-17

### Fixed

- `thlibo shorthand <file>` now honours the documented fail-closed
  contract on the backend-down path. Previously, when `thlibod`
  wasn't reachable the command exited 5 with empty stdout — a
  silent file-truncation hazard if the command was wired into a
  pre-commit hook via `--in-place`. Both backend-build and
  backend-run failures now route through a single `emitOriginal`
  helper that writes the original bytes to stdout (or no-ops the
  in-place rewrite, since the file already holds them) and exits
  0 with a stderr explanation. Two regression tests cover
  stdout-byte-identity and in-place mtime/size invariance.
- Built-in Python processors (`git-filter`, `npm-filter`,
  `cargo-filter`, `stacktrace-filter`, `pytest-filter`,
  `ndjson-filter`) now call `sys.stdout.reconfigure(newline="")`
  near the top of each script. Without this, Python 3's default
  text-mode stdout on Windows translated `\n` → `\r\n`, which
  broke byte-identity for callers that compared the script's
  output to its input. Local pipe tests confirm LF survives
  end-to-end through the rebuilt CLI on Windows.

### Documentation

- New "Output streams" section in `README.md` documenting the
  stdout-vs-stderr split, with a worked warning against
  `2>&1` merging (the background update-available banner is
  diagnostic, not data).

## [0.5.2] - 2026-05-16

### Fixed

- One-line installer (`install.sh` and `install.ps1`) now invokes
  `thlibo install --pull-engine --pull-model`, not just
  `--pull-model`. Previous behaviour left the user with a
  configured daemon that couldn't actually serve inference: the
  llamafile engine binary (~838 MB) was never downloaded, so
  every Bash tool call silently fell back to passthrough. Local
  test on Windows confirmed the engine now lands at
  `%LOCALAPPDATA%\thlibo\bin\thlibo-engine.exe` (~878 MB) on a
  fresh install, alongside the model GGUF in
  `~/.thlibo/models/`. The skip-the-configure-step path
  (`THLIBO_SKIP_INSTALL=1`) updated to reference the same flag
  pair so the manual fallback matches.

## [0.5.1] - 2026-05-15

### Fixed

- **#14**: `buildHookCommand` now wraps any `.ps1` hook path in
  `powershell -NoProfile -ExecutionPolicy Bypass -File "<path>"`,
  not just when `matcher == "PowerShell"`. The previous logic
  left the v0.3 Read matcher and the v0.4 Write/Edit matchers
  pointing at .ps1 paths directly on Windows; Claude Code's
  hook runner shells through bash there, and bash exploded
  trying to parse the PowerShell as shell on every invocation.
  Re-running `thlibo install` rewrites settings.json with the
  correct wrapper for all five matchers (Bash, PowerShell,
  Read, Write, Edit). Two regression tests added covering
  every matcher × {.sh, .ps1} combination.

## [0.5.0] - 2026-05-15

Two feature series landed back-to-back: v0.4 (`thlibo shorthand`,
auto-on-Write hook, YAML-aware mode, `thlibo config`) and v0.5
(four new family filters for the log-processing pattern). Built-in
processor count went 5 → 9. Notable side-fix: a latent `^`-without-
`(?m)` bug on git/npm/cargo filter regexes that meant they were
silently never firing via the fast-path matcher.

### Added — `pytest-filter`, `ndjson-filter` + case-file orchestration (v0.5 stages 2-4)

- New `processors/pytest-filter/` Python processor. Recognises
  pytest output via `=== test session starts ===` etc. Drops the
  env-info block (rootdir/configfile/plugins), drops the per-file
  dot-progress lines, keeps the FAILURES + ERRORS sections
  verbatim with their full tracebacks, keeps the short summary
  ("N passed, M failed in X.Xs"). 643 → 481 bytes on a small
  fixture; the win scales with project size.
- New `processors/ndjson-filter/` Python processor. Parses each
  line as JSON, groups by (level, msg), dedupes with a `_count`
  field on duplicates, sorts errors first. Preserves every
  distinct field of the first occurrence verbatim (host, query,
  callsite, version, request_id, etc.) so no load-bearing data
  is lost. **21,029 → 346 bytes (98.4%)** on a stream of 200
  records with one error repeated 197 times. Synonym-aware:
  level/severity/lvl, msg/message/body, OTel numeric severity.
- **Case-file orchestrator (stage 4) ships as the existing
  pipeline**, not as a new component. With the v0.5 family
  filters now correctly registered, `thlibo case <file>` runs
  through `casefile.Create → middleware.Pipeline.Process →
  registry.MatchFastPath` and the right filter dispatches by
  content shape — no router round-trip, no daemon needed for
  the deterministic path. The "orchestration" the post named is
  exactly what `MatchFastPath` does.
- Latent bug fix: the `match` regexes on `git-filter`,
  `npm-filter`, and `cargo-filter` used `^...` without `(?m)`,
  so they only matched at start-of-input rather than start-of-
  line. In practice this meant the fast-path was never firing
  for npm/cargo (they only ran via the `commands` allow-list at
  rewrite time). Added `(?m)` to all three. Also tightened
  `npm-filter` to require tree-glyph chars on the package-name
  alternation so it doesn't false-positive on plain bullet text.
- New `internal/middleware/family_dispatch_test.go` regression:
  TestFamilyDispatchByFastPath proves each of the 6 family
  filters dispatches correctly for representative fixtures, and
  TestUnknownContentFallsThrough proves plain prose returns nil
  (would route to the daemon's compress).

### Added — `stacktrace-filter` built-in (v0.5 stage 1)

- New `processors/stacktrace-filter/` Python script processor.
  Recognises Python `Traceback` blocks, Go `panic:` + goroutine
  dumps, Rust `thread '...' panicked`, Java exceptions / `Caused
  by:` chains, Node v8 stacks. Match regex anchors on the
  format-distinguishing first line.
- Compression strategy is **lossless for load-bearing parts**:
  exception messages preserved verbatim, every distinct file:line
  ref preserved, frame counts reported when frames are omitted.
- Dedupes consecutive identical frames before slicing — a 50-deep
  recursion of one frame collapses to "frame × 50" without
  consuming the head/tail budget. Then keeps first 3 + last 3
  frames + a "... N frames omitted ..." marker for the middle.
- Real-world measurement: 50-frame Python `RecursionError` trace
  goes from **2,360 → 261 bytes** (89% reduction) with the
  exception class, message, file, and line number all preserved.
- Bug fix: `processors/embed.go` `//go:embed` line was missing
  the v0.4 `shorthand` processor (latent — `BuildPipeline()` only
  found it via the user-mirrored copy at `~/.thlibo/processors/`,
  not the embedded fallback). Added both `shorthand` and
  `stacktrace-filter` and added a regression test
  (`TestEmbeddedFSContainsAllBuiltins`) so future built-ins can't
  silently miss the embed list.
- Two pre-existing tests updated for the new built-in count
  (5 → 7): `TestBuiltinsLoadedWithNoUserDir` named-list checks
  and `TestBuiltinsLoadedWithMissingUserDir` count assert.
- The C6 script-builtins integration test now also covers
  `stacktrace-filter` via a 50-frame Python recursion fixture.

Three more v0.5 stages tracked separately: pytest-filter,
ndjson-filter, and the case-file orchestrator that composes
the family filters per the post on
"Optimize Tokens Before They Hit the Cloud".

### Added — `thlibo config` interactive setup (v0.4 stage 4, completes v0.4)

- New `thlibo config` subcommand with four modes:
  - **interactive** (default) — Q&A walks each settings field,
    shows current value, accepts empty line to keep it, validates
    input, prints a diff, and asks to confirm before writing.
    Type `q` at any prompt to abort. `--yes` skips the final
    confirmation.
  - `--show` — print active config + source file path.
  - `--path` — print resolved config-file path (handy for
    scripting).
  - `--set key=value` — set one field non-interactively.
    Supports the four flat keys: `auto_shorthand_on_write`,
    `auto_shorthand_paths` (comma-separated globs),
    `auto_shorthand_min_bytes`, `auto_shorthand_yaml_prose`.
  - `--reset` — write `Defaults()` to the config file (with
    a confirmation prompt unless `--yes`).
- Config file gets a header comment so a user opening it later
  can find the docs: `# thlibo config — managed by 'thlibo config'`.
- 11 new tests covering interactive keep-current / toggle /
  abort flows, `--set` for each field type + invalid inputs,
  YAML round-trip via writeConfig→Load, `--show` output, diff
  reporter accuracy.

This completes the v0.4 shorthand feature series (stages 1-4).
You can now uninstall the standalone `/shorthand` skill — `thlibo
shorthand` is a strict superset, with a hard eval gate the skill
can't enforce, plus optional auto-on-Write and YAML-aware modes
the skill can't reach.

### Added — YAML-aware shorthand (v0.4 stage 3)

- New `Engine.CompressYAML` walks the YAML AST and rewrites only
  prose-shaped scalar values. Block-scalar style (`|` literal,
  `>` folded) is always prose at ≥80 chars; plain/quoted scalars
  must be ≥120 chars AND have ≥4 spaces (rules out
  identifiers, paths, regex patterns, version strings).
- Structural-key blocklist: `name`, `version`, `model`, `type`,
  `match`, `allowed_tools` / `allowed-tools` / `allowedTools`,
  `commands`, `command`, `entry`, `tools`, `id`, `uuid`, `sha`,
  `hash` — even long values under these keys are NEVER rewritten
  because they're identifiers, not prose.
- Per-scalar eval gate: each rewritten value individually passes
  through `Evaluate(original, compressed)`. Failed scalars revert
  to the original; the rest of the document still benefits from
  the safe rewrites.
- Doc-level eval gate: after all scalars are rewritten, the
  whole-document Compressed bytes pass through `Evaluate` once
  more. A doc-level failure reverts the entire compression — the
  user gets their original bytes back, never a partial rewrite.
- New `IsYAMLContent` heuristic detects YAML by content shape so
  the dispatch works even for files without `.yaml` extension
  (or stdin).
- `thlibo shorthand --yaml=auto|on|off` flag (default `auto`)
  routes through the YAML walker. Auto detects via file extension
  first, then content shape.
- Write/Edit hook respects `auto_shorthand_yaml_prose` config
  toggle (off by default — explicit opt-in because YAML walker
  has more semantic risk than flat prose).
- 6 new tests: IsYAMLContent heuristic, walker visits scalars
  only, allowed_tools list preserved through lowering backend,
  long description rewritten with eval pass, no-candidates =
  AlreadyShorthand, scalar eval failure reverts that scalar
  alone (other safe rewrites survive).

### Added — auto-on-Write hook (v0.4 stage 2)

- New `thlibo shorthand-hook` internal subcommand. Drives the
  Write/Edit PreToolUse hook: reads tool envelope from stdin,
  decides whether to rewrite, runs the shorthand engine, emits
  `hookSpecificOutput` JSON Claude Code substitutes. Always
  exits 0 — never breaks the AI client on failure.
- New embedded hook scripts: `hook-write.sh` (~25 lines,
  Bash) and `hook-write.ps1` (~22 lines, PowerShell). Same
  shell-thin pattern as the other hooks; all decision logic
  lives in the Go subcommand for testability.
- `internal/config` package: loads `~/.thlibo/config.yaml`
  (override via `$THLIBO_CONFIG`). `auto_shorthand_on_write`
  defaults to **off** — opt-in only. `auto_shorthand_paths`
  defaults to glob list `**/SKILL.md`, `**/CLAUDE.md`,
  `**/AGENTS.md`, `**/agents.md`, `**/.claude/skills/**/*.md`,
  `**/prompts/*.yaml`, `**/prompts/*.yml`. `auto_shorthand_min_bytes`
  defaults to 500 (smaller files passthrough).
- `claudecode.MergeSettingsAll` and `MergeHooks` struct: named-arg
  superset of `MergeSettingsWithRead`, registers the Write/Edit
  matchers (one script, two registrations — both tools land bytes
  on disk). The legacy positional-arg merge functions remain as
  thin wrappers.
- Originals preserved at
  `~/.thlibo/cases/shorthand/<sha-prefix>-<unix-ts>/{original,meta.json}`
  so a bad eval can be recovered manually. The hook **never**
  rewrites without a backup; if the backup write fails the new
  content goes through anyway with a stderr warning, but in
  practice the dir is readable and the backup lands.
- Installer now writes the four new hook scripts (Write/Edit
  Bash + PS1) and registers them via `MergeSettingsAll`. Output
  reminds the user the auto-rewrite is OFF by default.
- Uninstaller cleans up Write/Edit hook scripts + their `.new`
  conflict copies.
- 12 new tests across `internal/config` (defaults, env override,
  malformed YAML, glob matching) and `internal/adapters/claudecode`
  (Write+Edit registration, idempotence across multiple installs,
  RemoveHooks dropping Write/Edit while preserving unrelated
  user hooks).

### Added — `thlibo shorthand` (v0.4 stage 1)

- `thlibo shorthand <file>` compresses LLM-facing prose (SKILL.md,
  CLAUDE.md, agents.md, system prompts) into token-efficient
  shorthand. Modes: stdout (default), `--in-place` (with `.orig`
  backup), `--validate` (CI gate, exit 0/1), stdin via `-`.
- New `internal/shorthand` engine + `Evaluate()` safety gate.
  Fail-closed: if any of NEVER/MUST/SHALL/ALWAYS/DO NOT directives,
  fenced code blocks, frontmatter keys, URLs, file paths, version
  strings, or numeric thresholds don't survive the compression, the
  original bytes are emitted instead. Better to over-emit the
  original than silently drop a directive.
- New embedded `shorthand` prompt processor at
  `processors/shorthand/processor.md` with the full ruleset:
  preserve-verbatim list, eight ordered compression rules, four
  anti-patterns, output-format contract.
- 16 unit tests covering happy path, already-shorthand sentinel,
  backend errors, nil backend, and every eval-checklist failure
  mode (directive, code fence, frontmatter, URL, version, numeric
  threshold, file path, no-new-claims).
- Stage 2 (auto-on-Write hook), stage 3 (YAML-aware compression for
  `prompts/*.yaml`), and stage 4 (`thlibo config` interactive
  setup) tracked separately for follow-up commits.

## [0.3.0] - 2026-05-14

Feature-focused release building on v0.2's hardening foundation.
New Read-tool hook + `thlibo case` + `/caselog` skill turn dragged
log files into compressed case directories before Claude sees
them. Background update checker notifies users of new releases.
Gatekeeper + GITHUB_TOKEN installer fixes unblock fresh macOS
installs and rate-limited networks. All CI actions bumped to
Node-24-native majors where available.

### Added

- `thlibo case <file>` subcommand and `/caselog` Claude Code skill +
  PreToolUse Read-tool hook. Flow: when Claude's `Read` tool fires
  on a log-shaped file over ~32 KB, the hook calls `thlibo case`
  which writes `~/.thlibo/cases/<ts>-<hex>/{compressed.log,
  summary.md, meta.json}` and rewrites `tool_input.file_path` to
  the compressed variant. Claude sees the small version without
  the user having to pre-process. Users can also invoke the
  `/caselog` skill manually or run `thlibo case <file>` on the
  CLI. Gated on extension (`.log|.ndjson|.txt|.out|.err|.trace|
  .dump`), size (`THLIBO_READ_MIN_BYTES`, default 32 KiB), and
  honours `$THLIBO_DISABLED`. Falls back silently on any error so
  Claude always gets something to read. Includes
  `thlibo case --prune <duration>` to garbage-collect old cases.
  The skill SKILL.md is mirrored into
  `~/.claude/skills/caselog/SKILL.md` by `thlibo install`, using
  the same SHA-stamp / conflict-preservation semantics as hook
  scripts.
- Hook scripts survive `thlibo install` across updates. `WriteHookScript`
  and `WriteHookScriptPS1` now stamp the installed file with a SHA-256
  comment (`# thlibo-installed-sha: <hash>`). On reinstall: if the
  embedded version is unchanged the file is left untouched (Unchanged);
  if the file is pristine (user never edited it) it is overwritten with
  the new version (Updated); if the user modified the file the new
  version is written alongside as `<path>.new` and the user's file is
  preserved (Conflict). `thlibo install` prints a clear message for each
  case. Closes #12.
- Background update checker. `thlibo` CLI invocations (everything
  except `thlibo version`) fire a detached goroutine that fetches
  `api.github.com/repos/3rg0n/thlibo/releases/latest` once per
  cooldown window (default 24 h, cached at
  `~/.thlibo/state/update-check.json`). When a newer semver tag is
  available, prints a single banner to stderr pointing at the
  install-script upgrade command; the banner repeats on subsequent
  invocations only if the latest tag changes. Network failures are
  silent (logged at debug). Kill switches: `THLIBO_NO_UPDATE=1` or
  `THLIBO_UPDATE_INTERVAL=0`. Dev builds (untagged) never check.
- New `internal/version` package exposing the build tag via
  `-ldflags -X github.com/3rg0n/thlibo/internal/version.Tag=v…`;
  `release.yml` now injects `${{ github.ref_name }}` on every build.
- New `thlibo version` / `thlibo --version` subcommand prints the
  embedded tag without triggering the update check.

### Fixed

- Hook scripts written by an older installer (no SHA stamp) are now
  recognised as pristine and upgraded cleanly instead of triggering a
  spurious Conflict warning on first re-install. Closes #15.
- macOS Gatekeeper no longer blocks thlibo/thlibod on first run.
  `install.sh` strips `com.apple.quarantine` from both binaries after
  extraction; `PullEngine` does the same for the llamafile engine after
  download. Without this, macOS pops an "app blocked" toast and the
  daemon cannot start. Closes #13.
- `install.sh` now passes `GITHUB_TOKEN` (when set) as an
  `Authorization: Bearer` header to the GitHub releases API call that
  resolves `latest`. Machines that have exhausted the 60-req/hr
  unauthenticated rate limit were getting a 403 and the installer
  bailed before downloading anything. Explicit `THLIBO_VERSION=vX.Y.Z`
  still bypasses the API entirely. Closes #14.

- LaunchAgent plist now sets `HOME` env var and passes explicit `-engine`/`-model`
  flags so thlibod doesn't fail to resolve default paths under launchd's stripped
  environment. Daemon was crashing in a restart loop with `engine exited before
  ready: exit status 1` on every launchd-managed start. Closes #11.
- `THLIBO_LOG=1` baked into the plist so activity logs appear in
  `~/.thlibo/logs/thlibod.ndjson` without manual configuration.
- `install.sh` NEXT STEP 2 now instructs `--pull-engine --pull-model`
  together; the engine (~838 MB) is required and was silently omitted
  from the documented flow, causing `thlibod` to fail immediately on
  fresh installs with "no such file or directory". Closes #5.
- `thlibo install` plan output now clearly warns when the engine will
  not be downloaded ("thlibod will fail without it") instead of the
  neutral "not downloaded" message.
- Remove unused plist XML structs (`launchAgent`, `dictNode`, `kvNode`)
  in `internal/install/autostart_darwin.go` — leftover from an earlier
  draft before `plistXML()` switched to hand-rolled string formatting.
  Closes #10.

## [0.2.0] - 2026-05-14

Hardening + platform-coverage release. Closes every finding from the
MAESTRO threat model sweep, including the four originally marked
"Accepted by design" that were re-scoped as the `inferd` split
surfaced a multi-tenant story. Adds PowerShell tool support for
Claude Code on Windows, Gemma 4 native context-window + stop-token
flags for the daemon, and Sigstore keyless signing + CycloneDX SBOM
on every release artefact.

### Added (v0.2 hardening — #16, #17, #22, #24)

- `thlibo uninstall` subcommand reverses `thlibo install`: removes
  thlibo entries from `~/.claude/settings.json` via new
  `claudecode.RemoveHooks`, deletes hook scripts, unregisters
  autostart. `--purge` additionally deletes `~/.thlibo/` (processors,
  models, logs). `--dry-run` prints the plan without touching the
  filesystem. Closes finding #16.
- `$THLIBO_DISABLED=1` env gate honoured by `hook.sh`, `hook.ps1`,
  and Codex `hook.sh`. Per-session bypass without uninstalling.
- `queue.NewWithCallerCap` adds a per-caller concurrent-queued
  quota (default 4) on top of the global 10-slot limit. Exceeding
  the cap returns new `queue.ErrCallerFull`. Closes finding #17.
- `internal/execpolicy` package: loads `~/.thlibo/policy.yaml`
  (override via `$THLIBO_POLICY`), evaluates against `argv[0]`
  with deny-wins semantics and configurable default. `thlibo exec`
  calls it before spawn; denial returns exit 77 (`EX_NOPERM`).
  Closes finding #22.
- `ipc.PeerIdentity(net.Conn) (PeerID, error)` reads
  `SO_PEERCRED` on Linux and `GetNamedPipeClientProcessId` +
  `OpenProcessToken` on Windows. Darwin path returns a bare Unix
  transport identity with `UID=-1` until `LOCAL_PEERCRED` lands
  in v0.3. Daemon rejects UID/SID mismatches at accept time.
  Closes finding #24.

### Added (v0.2 feature work)

- **PowerShell tool support (#12).** Embedded `hook.ps1` companion to
  `hook.sh`; `MergeSettingsFull` registers a second PreToolUse
  matcher for `PowerShell` pointing at the new hook via
  `powershell -NoProfile -ExecutionPolicy Bypass -File <path>`.
  Installer writes both hooks unconditionally so Claude Code picks
  up whichever tool the current session uses
  (`CLAUDE_CODE_USE_POWERSHELL_TOOL=1` selects PowerShell).
- **Gemma 4 context window + stop tokens wired into the daemon (#13).**
  New `thlibod -ctx N` (default 32768, passed as `-c <N>`) and
  `thlibod -stop "<t1>,<t2>"` (default `<turn|>,<end_of_turn>`, each
  passed as `--stop <t>`) flags. Operator-supplied `-engine-args`
  appears after the built-in flags so last-value-wins overrides
  work.
- **Signed releases via Sigstore keyless (#27).** `release.yml` now
  runs `cosign sign-blob --yes` on every archive, `SHA256SUMS`, and
  the new SBOM. `.sig` + `.pem` uploaded alongside each asset.
  Identity = this workflow at the release tag; transparency log
  entries published to `rekor.sigstore.dev`.
- **CycloneDX SBOM on release (#28).** `anchore/sbom-action` emits
  `thlibo-sbom.cdx.json` at release time, pinned by commit SHA,
  signed with cosign alongside the other artefacts.

### Security

Second remediation pass sweeping every low-severity finding that is a
real bug (not a design decision). Combined with the first pass, the
remaining open items in `THREAT_MODEL.md` are the four explicitly
marked "won't-fix: by design" (queue-based rate limiting, exec
allow-list, SO_PEERCRED, persistent hook install) — #27 and #28 are
now closed.

### Added (second pass)

- Script-entry TOCTOU guard — `processors.EntryFingerprint`
  (size/mtime/mode) captured at registry load and re-verified at
  dispatch; a mismatch returns `processors.ErrEntrySwapped` and the
  middleware falls back. Closes finding #9.
- Rolling log rotation — keeps `.old`, `.old.1`, `.old.2` generations
  (configurable cap `maxRotatedGenerations`), preserving a forensics
  window that survives a second rotation. Closes finding #13.
- `processors.Strip` rewritten as a bounded state-machine parser
  with a `maxThoughtBytes = 64 KiB` cap on each block. Unclosed /
  oversized blocks now fall through as literal text instead of
  being eaten. Regression tests for stacked-open and oversized
  cases. Closes finding #19.
- `AcquireLock` rejects pre-existing symlinks at the lock path via
  `Lstat` + post-open `Stat.IsRegular()` check. Closes finding #21.
- Clarified `internal/install` package docstring: `thlibo install`
  does NOT create the `thlibo-users` group (v0.1 is per-user only).
  Closes finding #26.

### Changed (second pass)

- `SubprocessEngine.Generate` composes the child-request line via
  `json.Marshal` on an anonymous struct instead of `fmt.Sprintf`
  with `%q`. `%q` uses Go string-literal escape rules that don't
  match JSON for some edge cases (U+2028/2029, surrogate pairs).
  Closes finding #18.

### Security

Remediation sweep informed by the new MAESTRO threat model
(`THREAT_MODEL.md`). Each item links to the finding number it
addresses.

### Added

- `internal/promptsan` package — escapes Gemma 4 native tool-call
  markers (`<|`, `|>`) in untrusted tool output before it becomes a
  model-facing user turn. Used by both the middleware's prompt
  processors and the router. Closes finding #1.
- Dependency CVE pass via `govulncheck` returned 0 findings; wired a
  Dependabot config (`.github/dependabot.yml`) to track GitHub Actions
  SHA drift. Closes governance gap behind finding #2.
- `ipc.MaxRequestBytes` (64 MiB) per-frame cap with
  `ipc.ErrFrameTooLarge` on the daemon-side reader. Closes finding #5.
- `StartLimitIntervalSec=60 StartLimitBurst=3` + a full
  `NoNewPrivileges` / `ProtectSystem` / `ProtectHome` / `PrivateTmp` /
  `PrivateDevices` defence-in-depth block in the systemd user unit.
  Closes findings #6 and #14.
- `processors.ShadowWarning` emitted through the existing warnings
  channel when a user processor overrides a built-in of the same
  name. Closes finding #7.
- `logx.Redact` secret-pattern redactor applied to every `Str` /
  `Err` field (AWS keys, GitHub PATs, HuggingFace tokens, generic
  `*_TOKEN=` / `*_SECRET=` / `*_PASSWORD=` / `*_API_KEY=` assignments).
  Closes finding #8.
- Audit-trail NDJSON records for previously silent paths: daemon boot
  / ready / signal / stop, `thlibo pull` SHA mismatch or network
  failure, `parseRoutingResponseDetailed` surfacing unknown router
  names via `ClientAdapter.OnUnknownProcessor`. Closes findings
  #10, #11, #12.
- README "Security model" section explicitly names the auto-allow
  PreToolUse hook behaviour and points at `THREAT_MODEL.md` for the
  full trade-off discussion. Closes finding #15.
- `.gitignore` extra secret patterns: `*.jks`, `*.gpg`, `*.asc`,
  `*secret*`, `id_rsa*`, `id_ecdsa*`, `id_ed25519*`. Closes
  finding #20.

### Changed

- `verifySHA` now uses `crypto/subtle.ConstantTimeCompare` in place
  of the hand-rolled `equalFold` loop. Deleted `equalFold`. Closes
  finding #4.
- All GitHub Actions in `ci.yml`, `release.yml`, and `pages.yml` are
  pinned by commit SHA (with the semver tag preserved as a trailing
  comment). Closes finding #2.

## [0.1.0] - 2026-05-13

First release. A working local-Gemma compression middleware for
Claude Code + Codex CLI.

### Added

#### Daemon (`thlibod`)

- Newline-delimited JSON protocol with per-request `id` correlation,
  Gemma 4 sampling defaults (temperature 1.0, top_p 0.95, top_k 64),
  image-token-budget validation, `grammar` field for GBNF output
  constraints.
- Single-instance lock (`flock` on Unix, `LockFileEx` on Windows).
- Platform-specific IPC: Unix domain sockets with group + mode,
  Windows named pipes via `go-winio` with SDDL granting current-user
  only, TCP loopback fallback.
- Engine-agnostic `SubprocessEngine` abstraction + an in-repo
  `llamafile-stub` for tests. Ready-gated socket creation, graceful
  drain-and-exit on SIGTERM.
- Admission queue: 1 active generation, 10 queued by default,
  non-blocking `Submit` with `ErrFull` on overflow. Client disconnect
  cancels the in-flight job via context propagation.
- Engine supervisor: up to 3 lifetime restart attempts on llamafile
  crash; admin clients receive `restarting_engine_attempt_N` /
  `ready` status broadcasts and a terminal error on exhaustion.

#### Middleware (`thlibo`)

- Processor registry: YAML + markdown descriptors from embedded
  built-ins and `~/.thlibo/processors/`. User entries override
  built-ins by name. Strict YAML decoder rejects unknown fields;
  broken descriptors are quarantined with a warning instead of
  aborting the scan.
- Pipeline: 2000-byte short-circuit → fast-path regex match → daemon
  routing call → processor chain → compressed output. Every failure
  mode falls back to the original bytes (8-case fallback matrix).
- Router uses Gemma 4's native tool-call format
  (`<|tool_call>call:route{processors:[...]}<tool_call|>`) with a
  GBNF grammar that enforces the trained-for token pattern
  token-by-token.
- Mandatory thought-stripping: `processors.Strip` removes the
  `<|channel>thought…<channel|>` block Gemma emits before every
  answer (including the empty block when thinking is disabled),
  so model internals don't leak into the AI client's context.

#### Built-in processors

- Five embedded processors shipped via `go:embed` under
  `processors/`:
  - `git-filter` (script, Python) — `git status`/`diff`/`log`
  - `npm-filter` (script, Python) — `npm`/`npx`/`pnpm`/`yarn`
  - `cargo-filter` (script, Python) — `cargo build`/`test`/`clippy`
  - `compress` (prompt) — generic verbose-output summariser
  - `casefolder` (prompt, thinking-enabled) — stack traces, error
    logs, crash output

#### Client adapters

- **Claude Code** (`internal/adapters/claudecode`): PreToolUse hook
  that calls `thlibo rewrite` and emits `updatedInput` so the Bash
  tool runs a `thlibo exec -- <cmd>` wrapper instead of the raw
  command. `MergeSettings` is idempotent, preserves every unrelated
  key, refuses to overwrite malformed JSON, normalises Windows paths
  to forward slashes so `bash -c` doesn't eat the backslashes.
- **Codex CLI** (`internal/adapters/codex`): PostToolUse hook that
  replaces `tool_response` with a compressed version via
  `decision:block` + `reason`. Installer also enables
  `[features] codex_hooks = true` in `config.toml` (required or
  Codex silently ignores hooks) and merges `hooks.json`.

#### CLI

- `thlibo rewrite <cmd>` — registry lookup keyed on argv[0],
  exit-code protocol (0=rewrite / 1=passthrough / 2=deny reserved /
  3=ask reserved). Emits an absolute-path
  `thlibo exec --` prefix so the rewritten command runs under
  Claude Code's Bash tool without PATH inheritance.
- `thlibo exec -- <cmd>` — subprocess wrapper. Runs the command,
  captures stdout, pipes through `middleware.Process`, emits
  compressed stdout with stderr + exit code preserved verbatim.
- `thlibo compress` — read stdin, compress, write stdout. Used by
  the Codex hook and for shell pipelines.
- `thlibo install` — mirrors built-ins to disk, writes + merges the
  Claude Code hook, registers `thlibod` for per-user autostart
  (Windows Startup folder / macOS LaunchAgent / Linux systemd user
  unit). Optional `--codex`, `--pull-model`, `--allow-unpinned`,
  `--dry-run`, `--engine-path`.
- `thlibo pull [name]` — HTTPS-only GGUF downloader with HTTP Range
  resume, SHA-256 verification, progress indicator, context
  cancellation. Tests never hit the real network (httptest.Server).

#### Infrastructure

- GitHub Actions `ci.yml`: matrix build+test on ubuntu/macos/windows
  with Go 1.22; scanner job runs `staticcheck`, `govulncheck`,
  `gosec`, `semgrep --config=auto`; secrets job runs `gitleaks`.
- GitHub Actions `release.yml`: tagged-release workflow downloads
  the pinned GGUF once, computes its SHA-256, builds 4 platform
  bundles (linux-amd64/arm64, darwin-arm64, windows-amd64) with
  `-ldflags -X ...pinnedGemma4E4BQ4KM=<sha>`, attaches the GGUF as
  a release asset, publishes a draft release with SHA256SUMS.
- `DefaultModel.ExpectedSHA256` pinned to
  `51865750adafd22de56994a343d5a887cc1a589b9bae41d62b748c8bd0ca9c76`
  for `bartowski/google_gemma-4-E4B-it-GGUF/google_gemma-4-E4B-it-Q4_K_M.gguf`
  (5.4 GB). CI builds can override per-release via `-ldflags -X`.
- Token-savings measurements recorded in
  [.plan/release-notes-0.1.0.md](.plan/release-notes-0.1.0.md):
  97.6% on git diff, 99.4% on npm list, 89.2% on cargo test, 5.4%
  on git status.
- README with install/uninstall/customize/disable/security/limitations
  sections.
- 184 tests across the project. `staticcheck`, `govulncheck`,
  `gosec`, `gitleaks`, `semgrep` clean on shipped code.

### Changed

- Spec: request/response frames now carry a client-generated `id`
  field, echoed on every response. Admin status frames use
  `id: "admin"`.
- Spec: request envelope gained a `grammar` field for GBNF output
  constraints.
- Spec URL for the canonical GGUF corrected to
  `bartowski/google_gemma-4-E4B-it-GGUF` (earlier placeholder path
  did not resolve).

### Deferred

- **D3 — proxy mode (`ANTHROPIC_BASE_URL=...`).** Would cover
  `Read`/`Grep`/`Glob` and MCP tools that bypass the Bash-rewrite
  path. Every example in the spec's own token-savings table is
  Bash-produced, so v0.1 ships without it. v0.2 candidate.

### Not needed

- **E1 — shared `thlibo-users` group.** Per-user autostart model
  has the daemon running as the invoking user, with an IPC ACL
  already scoped to the current user's SID on Windows. No shared
  group required. Gate row kept struck-through as a deliberate
  decision, not an oversight.
