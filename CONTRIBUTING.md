# Contributing to thlibo

Thanks for your interest. Thlibo is small and pragmatic — this doc
is correspondingly short. If anything here seems wrong for your
situation, open an issue and we'll sort it.

## Quick start

```bash
git clone https://github.com/3rg0n/thlibo.git
cd thlibo
go build ./...
go test ./... -timeout 180s
```

If you're on Windows, see the README for the `.exe` naming.

## Before you send a PR

1. **`go test ./... -timeout 180s`** — the full suite. The daemon
   integration tests take ~70 seconds on a dev box; don't let that
   convince you to skip them.
2. **`go vet ./...`** — must pass silently.
3. **`staticcheck ./...`** — must pass silently. Install with
   `go install honnef.co/go/tools/cmd/staticcheck@latest`.
4. **`gosec ./cmd/... ./internal/... ./processors/...`** — must
   pass silently. Medium+ findings get a `#nosec` comment with
   justification or get fixed.
5. **`govulncheck ./...`** — no known vulnerabilities in
   dependencies.
6. CI re-runs all of the above plus `semgrep --config=auto` and
   `gitleaks` on every push and PR.

## Commit hygiene

- One logical change per commit. Don't mix refactors with features.
- Commit messages: short imperative subject, blank line, then a
  body that explains the *why*. Don't describe what changed in the
  subject — the diff does that. Describe why the change is right.
- Run the test suite before every commit, not just before the PR.
  A green `git bisect` is cheap insurance.

## Where to start

- Open issues tagged `good first issue` or `help wanted` are
  designed to be standalone chunks with clear boundaries.
- Fixing a typo, adding an example, improving an error message —
  all welcome, no forum post needed.
- Adding a new **processor** (script or prompt) is the lowest-risk
  kind of contribution. See README §Customise and the existing
  built-ins under `processors/` for templates.

## Larger changes

For anything that touches:

- The IPC protocol (`internal/ipc/protocol.go`)
- The daemon lifecycle or supervisor
- The router grammar / parser
- The installer's autostart or settings-merge logic

please open an issue first describing the shape of the change
before opening a PR. That's not bureaucracy — it's so we can catch
direction disagreements before you sink hours into a specific
implementation.

## Architectural decisions

Cross-cutting decisions (protocol changes, adapter mechanisms,
security model shifts) belong in an ADR under `docs/adr/`. See
`docs/adr/README.md` for the index and `docs/adr/0001-*.md` for an
example. You don't need to write one for a small bug fix or a new
processor; you do need one if you're changing *how the parts fit
together*.

## Scope — what goes in, what doesn't

**In scope for thlibo proper:**

- Everything that makes the existing pipeline faster, safer,
  smaller, or more observable.
- New built-in processors for additional dev tools.
- New client adapters (IDE agents, SDK wrappers, CLI-shell tools).
- Cross-platform parity improvements.
- Security hardening.

**Probably out of scope:**

- Bundled LLMs other than Gemma 4 E4B. The stack is designed
  around one warm model. Supporting a second family would add a
  lot of configuration surface without a clear win for the
  compression use case.
- Multi-turn conversation state. v0.1 is explicitly single-turn;
  changing that is a big design shift.
- Telemetry / analytics / metrics export to cloud services.
  thlibo's value proposition is "nothing leaves your machine".

If in doubt, open an issue asking.

## Security and disclosure

If you find something that looks like a security issue, please
report it via GitHub's private security advisory mechanism on the
repo rather than a public issue. We'll acknowledge within a
reasonable timeframe and coordinate disclosure.

## License

By contributing, you agree that your work is licensed under the
same MIT license the rest of the project uses (see `LICENSE`).
