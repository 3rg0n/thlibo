# 0008. numpy as a processor dep, vectors over `array` + math

- Status: accepted
- Date: 2026-05-23

## Context

The `cordon-filter` processor (issue #28) surfaces semantically
rare windows in a log stream. The inputs are 256-dim float vectors
returned from inferd's embed endpoint; the work is k-NN density
scoring (mean Euclidean distance to the K nearest neighbours).

For a log of N windows, the brute-force step is an N×N pairwise
squared-distance matrix, then `np.partition` to grab the k smallest
distances per row. With cordon-filter's 5 000-window cap (per
`CORDON_MAX_WINDOWS`), that's a 25 M-cell matrix, computed once.

The decision: take a `numpy` runtime dependency for the
processor, or stay stdlib-only by re-implementing the math in
Python loops + the `array` module.

This is the first non-stdlib runtime dep any thlibo processor has
introduced. Existing Python processors (`compress`, `casefolder`,
`git-filter`, `npm-filter`, `cargo-filter`, `stacktrace-filter`,
`pytest-filter`, `ndjson-filter`) all run on the stdlib only. The
`pdf-to-md` processor (per ADR 0007) introduced `pypdf` +
`pdfplumber`, but those are PDF-format dependencies — there's no
stdlib path that does the same job.

## Decision

**Take the numpy dep, soft-import.** `cordon-filter/run.py` does:

```python
try:
    import numpy
except ImportError:
    return raw
```

If numpy isn't installed, the processor falls through to passthrough
— the AI client sees the unmodified input, exactly as if cordon
had never been invoked. No crash, no error message. This honours
the load-bearing invariant from `CLAUDE.md`: *"Fallback to original
output on any error path. The middleware must never break the AI
client."*

The math itself stays the textbook implementation:

```python
arr = np.asarray(vectors, dtype=np.float32)
sq = ((arr[:, None, :] - arr[None, :, :]) ** 2).sum(axis=-1)
np.fill_diagonal(sq, np.inf)
nearest_k = np.partition(sq, k, axis=1)[:, :k]
return np.sqrt(nearest_k).mean(axis=1).tolist()
```

Five lines that compile to vectorised C; a stdlib equivalent is
~40 lines of nested loops at roughly 100× the wall time on
realistic input.

`scipy.spatial.KDTree` was considered as a potential speedup over
brute force, but for N ≤ 5 000 the O(N²) brute path completes in
<100ms, and pulling scipy in for a single tree query when numpy
already does the job would double the install footprint. Brute
force is also more transparent: anyone reading the code sees the
distance matrix, not a tree traversal.

## Consequences

**Easier:**

- The math reads like the math. A reviewer who knows k-NN sees
  exactly what's happening; they don't have to mentally vectorise
  Python loops.
- numpy is the lingua franca of Python numerics — anyone landing
  in this file has a non-zero chance of recognising the shape.
- O(N²) over 25 M cells in float32 takes <100ms on a modern laptop.
  A pure-Python equivalent over the same input takes ~10s. For a
  hot-path processor that runs on every Bash tool invocation, that
  difference matters.
- numpy is already on most developer machines (jupyter, pandas,
  any data-science adjacent work pulls it in). Most users won't
  even notice the soft dep; for them it's a zero-cost addition.

**Harder:**

- One more thing for users to install. We document this in the
  processor's `README.md` and `processor.yaml` description: install
  with `pip install numpy`. Users on a stripped Python without it
  see passthrough output and the install instructions in the
  processor docs.
- numpy is a ~25 MB install; not zero. For users who care about
  install footprint and don't want cordon-filter, this is an extra
  cost they pay for nothing if they have it installed already (none)
  or an extra `pip install` step they don't want (one-shot).
- Sets a precedent. Future processors that want numpy/pandas/scipy
  for similar reasons now have a path to opt in via the same soft-
  import + passthrough pattern. Acceptable, but we should resist
  reaching for the dep when the stdlib + a few lines would do.

**Reversible if needed:**

- Swapping to a pure-Python implementation is a single-file change
  in `processors/cordon-filter/run.py`. The wire format, the embed
  endpoint, the output shape — none of that changes. The
  performance hit (~100×) means cordon would only be useful on
  smaller inputs, but the code change is local.
- Swapping to JAX or PyTorch is also local, but rejected: both
  are an order of magnitude heavier than numpy and we're not doing
  GPU work or autograd. numpy is the right size.

## Soft-import precedent

This pattern — `try: import X / except ImportError: return raw` —
is now the documented way for thlibo processors to take optional
deps. The processor's `processor.yaml` should call out the dep in
its `description:` field; the README explains how to install it.
The processor itself never crashes when the dep is missing.

The same pattern applies to:

- `cordon-filter` — numpy (this ADR)
- `pdf-to-md` — pypdf, pdfplumber (per ADR 0007)
- Future: any processor that would benefit from a non-stdlib lib
  but where graceful passthrough is acceptable behaviour.

It does **not** apply to:

- The thlibo daemon or middleware itself (single binary, no
  Python in the runtime path).
- Processors where the dep is structural (e.g. a hypothetical
  Rust-bindings processor where missing the dep means no
  processor).

## References

- Issue #28: cordon-filter scaffold
- Cordon (Apache-2.0, the algorithmic blueprint that uses
  torch + sentence-transformers; we deliberately don't):
  https://github.com/calebevans/cordon
- numpy: https://numpy.org/ (BSD-3, ubiquitous)
- ADR 0007: pdf-to-md processor (the other soft-import precedent)
- CLAUDE.md invariant 3: "Fallback to original output on any
  error path. The middleware must never break the AI client."
