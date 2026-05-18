# Shared Local Model Store — Cross-Tool Convention Proposal

A working draft of an upstream proposal: a shared, content-addressable
on-disk location for downloaded inference model weights. Respecting
XDG on Linux, Apple's Application Support layout on macOS, and
Microsoft's `%LOCALAPPDATA%` on Windows.

The intent is to stop every local-AI tool from re-downloading the
same multi-gigabyte weight file into its own private directory.

This document is structured for two audiences. **Sections 1–4** are
the public RFC — reusable as the body of a GitHub issue or spec
proposal on any of the upstream projects listed in Section 5.
**Sections 5–7** are the rollout plan: where to file, what order,
and how to migrate when the convention starts to land.

---

## 1. Stakes — what duplication costs today

A user with four common local-AI tools installed (one runtime, one
desktop UI, one HuggingFace-based Python project, one experiment)
holds the same 4–40 GB weight files four times on disk. The same
SHA, four copies, four directories.

A worked example for a single user running Ollama + LM Studio + a
Python project + one llama.cpp experiment, all using
`Llama-3.1-8B-Instruct.Q4_K_M.gguf` (4.92 GB):

| Tool | Path | Size |
|------|------|------|
| Ollama | `~/.ollama/models/blobs/sha256-…` | 4.92 GB |
| LM Studio | `~/.cache/lm-studio/models/.../…Q4_K_M.gguf` | 4.92 GB |
| HF Transformers | `~/.cache/huggingface/hub/models--…/blobs/…` | 4.92 GB |
| llama.cpp build | `~/projects/llama-experiment/models/…` | 4.92 GB |

**Disk: 19.7 GB.** Network: four downloads, often the same week.
On metered or capped connections that's a real bill.

Scale that up to a 70 B Q4_K_M (~40 GB), and the tax becomes
prohibitive on consumer SSDs. Many users simply pick one tool and
abandon the rest.

> **Key idea:** model weights are content-addressable, byte-
> identical across runtimes, and read-only at runtime. There is no
> technical reason they can't be shared. The duplication is a
> coordination failure, not a constraint.

---

## 2. Why now

Three things make this the right moment to propose a shared store:

- **Format consolidation.** GGUF dominates llama.cpp-family
  runtimes. safetensors covers the HuggingFace ecosystem. ONNX,
  MLX, and TensorRT engine plans round it out. The set is small
  enough to enumerate.
- **Existing precedent.** XDG already blesses category dirs for
  shared assets at `~/.local/share/`: fonts, icons, applications,
  themes. Each was ratified by a sibling spec, not by amending
  XDG itself. Models are the same shape: large, content-
  addressable, consumed by a class of tools.
- **Quiet existing implementations.** Ollama's blob store is
  already content-addressable internally. HuggingFace Hub already
  uses SHA-keyed blobs. Most of the engineering work for a shared
  layer has already happened; what's missing is a path convention
  the tools agree on.

> **Key idea:** the work is mostly done inside individual tools.
> The convention is a lightweight coordination layer on top.

---

## 3. The convention

### 3.1 Where the store lives

| Platform | Path | Reasoning |
|----------|------|-----------|
| **Linux / *BSD** | `${XDG_DATA_HOME:-$HOME/.local/share}/models/` | Per the XDG Base Directory Specification: user-specific data. Category dir at the same level as `fonts/`, `icons/`. |
| **macOS** | `~/Library/Application Support/models/` | Apple HIG: persistent user data not in iCloud. Distinct from `~/Library/Caches/`, which the system may purge under disk pressure. |
| **Windows** | `%LOCALAPPDATA%\models\` (e.g. `C:\Users\<u>\AppData\Local\models\`) | Microsoft guidance for non-roaming user data. **Critical:** never `%APPDATA%` (Roaming). Roaming profiles upload `%APPDATA%` to the domain controller / OneDrive — a 5 GB GGUF would replicate to every machine the user signs into and frequently exceed per-file size limits. |

Override: a single environment variable `MODELS_HOME`, matching
the existing `HF_HOME` / `OLLAMA_MODELS` pattern. When unset, the
platform default above applies.

### 3.2 Layout — content-addressable, manifest-mediated

```
$MODELS_HOME/
├── blobs/
│   └── sha256/
│       ├── ab/                              # 2-char fanout
│       │   └── abcd1234.../
│       │       └── data                     # raw weight bytes
│       └── cd/
│           └── cdef5678.../
│               └── data
├── manifests/
│   ├── llama-3.1-8b-instruct-q4_k_m.json
│   └── meta-llama-3-70b-instruct-q4_0.json
└── locks/                                   # advisory flock dir
```

**Blob path** is `blobs/sha256/<aa>/<full-hash>/data`, where
`<aa>` is the first two characters of the SHA. The fanout keeps
any one directory under ~1 % of its hash space, which matters on
filesystems that scale poorly past tens of thousands of entries.
This pattern matches Ollama's existing internal layout and Git's
loose-object store.

**Manifest** is a small JSON file naming what a blob is. Producers
write it. Consumers read by name and dereference to the blob:

```json
{
  "schema_version": 1,
  "name": "llama-3.1-8b-instruct-q4_k_m",
  "format": "gguf",
  "blob": "sha256:abcd1234ef567890...",
  "size_bytes": 4920739328,
  "license": "llama-3.1-community",
  "source": {
    "registry": "huggingface.co",
    "repo": "TheBloke/Llama-3.1-8B-Instruct-GGUF",
    "revision": "main",
    "filename": "llama-3.1-8b-instruct.Q4_K_M.gguf"
  },
  "produced_by": "ollama/0.3.12",
  "produced_at": "2026-05-18T14:32:00Z"
}
```

Different teams' repacks of the "same" model — Unsloth's IT-tuned
Q4_K_XL, Bartowski's Q4_K_M, an official Ollama push — get
**different manifests but share the blob if and only if their
bytes match**. Manifests are cheap (a few KB); blobs are the heavy
file. The split is the win.

> **Key idea:** consumers reference blobs by SHA, never by guessed
> filename. Names are display labels owned by manifests.

### 3.3 The read/write contract

The store is safe to share because the operations are designed
to be lock-free for readers and atomic for writers.

**Producer** (any tool downloading a model):

1. Stream the download to `blobs/sha256/<aa>/.partial-<hash>/data.tmp`.
2. Verify the SHA against the expected value.
3. `rename(2)` into `blobs/sha256/<aa>/<hash>/data` — atomic on POSIX, atomic-enough on NTFS via `MoveFileEx`.
4. Write `manifests/<name>.json` last.
5. Hold `locks/<name>.lock` (`LOCK_EX`) during steps 1–4 to block concurrent producers writing the same name.

**Consumer** (any tool loading a model):

1. Read `manifests/<name>.json`.
2. Verify a blob exists at the named SHA.
3. `mmap` the blob; never write to it.
4. Optionally re-verify SHA on first load (paranoid mode).

Compatible with NFS, SMB, and tmpfs. Lock-free for readers means
ten consumers can mmap the same 40 GB blob simultaneously without
contention.

### 3.4 Trust boundary

A shared dir is a shared attack surface. The mitigation is
already in the layout: **consumers reference blobs only via
manifests, and manifests bind a name to a SHA**.

- A malicious file dropped at an arbitrary path inside `blobs/` is
  invisible to consumers — no manifest points at it.
- A swapped blob reference inside an existing manifest is
  detectable at first load: the manifest carries `source.registry`
  + `source.repo` + `source.revision`, so the consumer can re-
  verify against the upstream registry.
- The store is per-user (`$XDG_DATA_HOME` is per-user by
  definition). Cross-user attacks require POSIX permissions
  failure, not a flaw in this convention.

> **Key idea:** content addressing is the security model. Don't
> trust the filename; trust the SHA the manifest names.

### 3.5 Multi-user / system-wide stores

Out of scope for v1, but the conventional locations are:

- Linux: `/var/lib/models/`
- macOS: `/Library/Application Support/models/`
- Windows: `%PROGRAMDATA%\models\`

Apps consult `$MODELS_HOME` first, then the system path. System
paths are admin-writable, world-readable.

---

## 4. Why this is XDG-shaped, not a new standard

`~/.local/share/fonts/`, `~/.local/share/applications/`, and
`~/.local/share/icons/` are not in the XDG Base Directory
Specification proper. They were established by sibling
freedesktop specs — Font config, Desktop Entry, Icon Theme — that
each settled on `$XDG_DATA_HOME/<category>/`.

This proposal is the same shape: a category dir, used by a class
of consumer (local-AI runtimes), respecting XDG on Linux with
platform-correct equivalents elsewhere. **It does not require
amending the XDG Base Directory Specification itself.** Adoption
by tooling is what creates the convention. A future spec amendment
follows the de facto reality, the same way the existing category
dirs did.

> **Takeaway for sections 1–4:** A shared model store at
> `~/.local/share/models/` (and platform equivalents), content-
> addressable with manifest indirection, is technically already
> possible — every runtime would need to honour one env var and
> agree on a layout that several already use internally.

---

## 5. Where to file the upstream issue, in order

The order is "where adoption has the most leverage," not "easiest
to convince." Lead with the tool whose adoption unblocks others.

| # | Repo | Ask | Risk |
|---|------|-----|------|
| 1 | **`ollama/ollama`** | Add `OLLAMA_BLOBS_DIR` env var, separate from `OLLAMA_MODELS` (which today holds both manifests and blobs). Default keeps current behaviour. | **Low.** Additive env var. Internal layout already content-addressable. |
| 2 | `ggml-org/llama.cpp` | Document a recommended path for `--model` defaults; add `LLAMA_MODELS_HOME` env. | **Low.** Convention only. |
| 3 | `Mozilla-Ocho/llamafile` | Same shape as llama.cpp. | **Low.** |
| 4 | `huggingface/huggingface_hub` | Add a "shared cache" mode that writes into `$MODELS_HOME` instead of `~/.cache/huggingface/hub/`. | **Medium.** HF cache layout is its own ecosystem with auth state and revision pinning. |
| 5 | LM Studio | Forum / Discord feature request — closed source. | **High.** Maintainer prerogative; may decline. |
| 6 | `nomic-ai/gpt4all` | Same as llama.cpp. | **Low.** |
| 7 | `gitlab.freedesktop.org/xdg/xdg-specs` | "New sibling spec for shared model store, modelled on Font config." | **Long-tail.** Freedesktop process is slow; expect months to years. Don't gate adoption on this. |

XDG specs themselves are maintained at
`gitlab.freedesktop.org/xdg/xdg-specs`. New conventions go through
merge requests and the `xdg@lists.freedesktop.org` mailing list.
Realistic timeline for any spec change is months at minimum. The
right play is to ship the convention by getting runtimes to
adopt it, then let freedesktop ratify the de facto state.

> **Key idea:** Ollama first. Their content-addressable plumbing
> already exists; exposing the path makes them the de facto host
> of the convention, which is a strategically good place to be.

---

## 6. Migration

Existing tool installs already have weights at private paths. A
clean migration plan, applicable to any tool adopting the
convention:

1. **First release with adoption**: write new pulls into
   `$MODELS_HOME`. Fall back to the legacy private path for reads.
   Log a one-line deprecation warning when a load served from the
   legacy path.
2. **One release later**: ship a `--migrate-paths` (or equivalent)
   subcommand that *moves* (not copies) the blob from the legacy
   path into the new location and writes the manifest.
3. **One release later**: stop writing to the legacy path. Continue
   reading.
4. **One release later**: drop the legacy fallback.

This is conservative on purpose. 5–40 GB blobs are non-trivial to
re-download on metered connections; never auto-migrate without
explicit consent.

---

## 7. Open questions

Worth raising in any upstream issue, not yet answered here:

- **Concurrency on Windows.** `rename(2)` semantics differ from
  POSIX. Validate `MoveFileEx` with `MOVEFILE_REPLACE_EXISTING`
  for atomicity guarantees.
- **Garbage collection.** When does an orphaned blob (no manifest
  references it) get reclaimed? The convention should specify
  who's responsible — the runtime that pulled it, a separate `gc`
  command, or the user explicitly.
- **Quantisation discovery.** Manifests carry the format but not
  the quantisation level. Should `q4_k_m` / `q5_k_s` / etc. be a
  first-class field, or part of `name`? Affects how UIs render
  "alternatives for this model" lists.
- **Signed manifests.** Future: cosign-style signature on
  manifests so a consumer can pin "I trust manifests signed by
  X." Not v1, but the schema should leave room.
- **Cross-runtime `mmap` compatibility.** Some runtimes mmap with
  expectations about page alignment. Validate that a single blob
  produced by one runtime is readable by all others.

---

## Appendix A — Ollama issue body (copy-pasteable)

```markdown
**Title:** Support a shared content-addressable model store via
`OLLAMA_BLOBS_DIR` (XDG / `%LOCALAPPDATA%` aware)

## Problem

Ollama already uses content-addressable storage for model blobs
under `OLLAMA_MODELS/blobs/sha256-<hash>`. That store is private
to a single Ollama install — every other local-AI tool the user
runs (llama.cpp, llamafile, LM Studio, HuggingFace Transformers,
custom apps) re-downloads the same GGUF into its own directory.

A user running four local-AI tools commonly holds the same
4–40 GB weight file four times. The blobs are byte-identical;
the duplication is a coordination failure, not a technical
constraint.

## Proposal

Split the existing `OLLAMA_MODELS` env var into two:

- `OLLAMA_MODELS` — Ollama's manifests directory. Default
  `$HOME/.ollama/models/`. **Unchanged.**
- `OLLAMA_BLOBS_DIR` — **new.** Controls only the `blobs/`
  subdirectory.
  - Default: `$OLLAMA_MODELS/blobs` (preserves current layout —
    no behaviour change for users who don't set it).
  - Recommended shared-store override:

    | Platform | Recommended `OLLAMA_BLOBS_DIR` |
    |----------|---------------------------------|
    | Linux / *BSD | `${XDG_DATA_HOME:-$HOME/.local/share}/models/blobs` |
    | macOS | `$HOME/Library/Application Support/models/blobs` |
    | Windows | `%LOCALAPPDATA%\models\blobs` |

  Windows note: do NOT point this at `%APPDATA%` (Roaming).
  Roaming profiles upload `%APPDATA%` to the domain controller
  or OneDrive — a 5 GB GGUF would replicate across every machine
  and frequently fail per-file size limits. `%LOCALAPPDATA%` is
  the correct bucket for large per-machine caches.

A user who sets the platform-appropriate path can then point
other tools at the same location and avoid re-downloading the
same weights.

This is a purely additive change. Default behaviour is unchanged.

## Why now

Local-AI tooling has converged enough on GGUF that a shared
blob store is finally a sensible thing to build. Ollama is the
only major tool that already has the content-addressable
plumbing — exposing the path makes Ollama the de facto host
for the convention, which is a strategically nice place to be.

## What I'd file separately

A companion proposal for a manifest layout neutral enough that
non-Ollama tools can publish into the same blob store. Happy to
take that to a separate issue or RFC if maintainers prefer to
keep this one focused on the env var split.

## Implementation sketch

```go
// envconfig/config.go
func BlobsDir() string {
    if s := Var("OLLAMA_BLOBS_DIR"); s != "" {
        return s
    }
    return filepath.Join(Models(), "blobs")
}
```

Then update every read/write of `filepath.Join(models, "blobs",
...)` to call `BlobsDir()` instead. ~30 callsites by my read of
`server/`, `runtime/`, and the registry packages.

Happy to send a PR if the direction is approved.
```

---

## Appendix B — Quick-reference path table

| Asset | Linux | macOS | Windows |
|-------|-------|-------|---------|
| Shared model blobs | `~/.local/share/models/blobs/` | `~/Library/Application Support/models/blobs/` | `%LOCALAPPDATA%\models\blobs\` |
| Shared model manifests | `~/.local/share/models/manifests/` | `~/Library/Application Support/models/manifests/` | `%LOCALAPPDATA%\models\manifests\` |
| Per-app config | `~/.config/<app>/` | `~/Library/Application Support/<app>/` | `%APPDATA%\<app>\` |
| Per-app cache | `~/.cache/<app>/` | `~/Library/Caches/<app>/` | `%LOCALAPPDATA%\<app>\cache\` |
| Per-app state / logs | `~/.local/state/<app>/` | `~/Library/Logs/<app>/` (logs) | `%LOCALAPPDATA%\<app>\state\` |
| Per-app runtime | `/run/user/$UID/<app>/` | `$TMPDIR/<app>/` | `\\.\pipe\<app>-*` |
