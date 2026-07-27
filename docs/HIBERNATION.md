# Hibernation

Hercules can compact branch-local analysis state when the execution plan will not use that branch
again for a while. This reduces peak memory on histories with many long-lived branches at the cost
of compression, serialization, and restoration work.

Hibernation scheduling is disabled by default. Enable it with:

```bash
hercules --hibernation-distance 10 <analysis flags> <repository>
```

`--hibernation-distance N` is the minimum number of execution-plan actions between two uses of a
branch before Hercules inserts a hibernate/boot pair. The distance counts commit, fork, and merge
actions rather than wall-clock time. `0` disables scheduling; a larger value hibernates less often.
Start around `10` and measure both memory and runtime for the target repository.

## Line history and modern burndown

The default `--burndown` analysis consumes the shared line-history stage. For large repositories,
configure both stages:

```bash
hercules --burndown \
  --hibernation-distance 10 \
  --lines-hibernation-threshold 200000 \
  --lines-hibernation-disk \
  --burndown-hibernation-disk \
  <repository>
```

- `--lines-hibernation-threshold N` sets the minimum allocated line-history state to compress in
  each branch. `0` disables line-history allocator compression. Lower positive values save memory
  sooner but use more CPU.
- `--lines-hibernation-disk` stores hibernated line-history allocators in temporary files instead
  of retaining their compressed bytes in memory.
- `--lines-hibernation-dir PATH` selects the temporary directory for those files.
- `--burndown-hibernation-disk` stores the modern Burndown leaf's hibernated state in a temporary
  file instead of memory.
- `--burndown-hibernation-dir PATH` selects the temporary directory for Burndown state.

The disk flags do not schedule hibernation by themselves; `--hibernation-distance` must be positive.
Temporary files are removed during boot and disposal, including failure paths.

The `--preset large-repo` preset selects the supported starting values
`--lines-hibernation-threshold=200000` and `--lines-hibernation-disk`, together with first-parent
traversal and coarser output sampling. It does not choose the scheduling tradeoff for you; pass a
positive `--hibernation-distance` as well.

## Legacy burndown

`--legacy-burndown` has its own allocator and therefore uses namespaced flags:

```bash
hercules --legacy-burndown \
  --hibernation-distance 10 \
  --legacy-burndown-hibernation-threshold 200000 \
  --legacy-burndown-hibernation-disk \
  <repository>
```

The optional directory flag is `--legacy-burndown-hibernation-dir`. The legacy disk option requires
a positive legacy threshold. These flags do not apply to the default `--burndown` implementation.
