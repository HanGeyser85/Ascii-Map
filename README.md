# Ascii-Map

A seeded, ASCII-rendered maze generator with forced bidirectional
teleporters and ten structurally different generation styles, plus a
benchmark harness where different pathfinding algorithms compete to find
the key and reach the exit the fastest.

## Build

```bash
go build -o maze.exe .
```

## Generate and solve one maze

```bash
./maze.exe -seed 42 -size normal -teleporters 2
```

Size presets: `-size normal` (21x15, the default), `-size large` (100x100),
and `-size xlarge` (250x250). Explicit `-width` and `-height` flags override
either dimension of the selected preset.

Every registered algorithm runs against the same maze, each getting its own
untouched clone. A leaderboard (ranked by score - see Scoring below, smallest
first) prints to the console, then the full results (every algorithm's path,
its search tree, and CPU/memory/timing) are written to a JSON file - open
`viewer.html` in a browser and load that file to explore them visually (see
Results viewer below) instead of scrolling a wall of ASCII art. Pass
`-console-routes` if you want the old inline ASCII panels instead of (or
alongside) the JSON file.

Flags:

| Flag               | Default   | Meaning                                                              |
| ------------------ | --------- | --------------------------------------------------------------------- |
| `-seed`            | 42        | RNG seed - same seed + dimensions + style = identical maze            |
| `-width`           | 21        | maze width in cells (min 5)                                           |
| `-height`          | 15        | maze height in cells (min 5)                                          |
| `-size`            | "normal"  | map-size preset: `normal` (21x15), `large` (100x100), or scary `xlarge` (250x250); explicit width/height override it |
| `-teleporters`     | 2         | number of teleporter pairs                                            |
| `-style`           | "Braided" | maze generation style; `-list-styles` prints every registered one     |
| `-braid`           | 0.15      | probability of knocking down an extra wall to create a loop (Braided only) |
| `-min-routes`      | 2         | minimum edge-disjoint routes to the key and to the exit (Braided only) |
| `-summary-only`    | false     | skip the ASCII maze render; show only the leaderboard                 |
| `-console-routes`  | false     | also dump every solved maze as ASCII panels to the console             |
| `-out`             | (auto)    | path for the JSON results file (`maze_result.json` / `bench_results.json`) |
| `-no-color`        | false     | disable ANSI color output (auto-disabled anyway when not a terminal)  |
| `-console-width`   | 0 (auto)  | override detected console width for side-by-side maze panels          |
| `-list-styles`     | false     | print every registered maze style and exit                            |
| `-bench`           | false     | ignore the flags above and run the fixed 10-seed, 10-style benchmark  |

## Benchmark mode

```bash
./maze.exe -bench
```

Regenerates the same 10 hardcoded seeds (`seeds.go`), each paired with a
different maze style (see below), so results are repeatable across runs and
machines - printing progress to stderr as it goes, since generating and
solving all 10 can take a few seconds. Prints every algorithm's average
score across all 10 seeds to the console, then writes the complete results
(every algorithm x every maze: paths, search trees, CPU/mem/timing) to
`bench_results.json`. `run.bat` runs this and opens `viewer.html`
automatically.

## Results viewer

`viewer.html` is a self-contained page (no server, no build step) for
exploring a results JSON file: drag it in, or click "Open JSON...". It
shows:

- A leaderboard, sortable by any column.
- Every maze from the run, selectable from the sidebar.
- Any combination of algorithms for the selected maze, shown side by side.
- An animated replay of each algorithm's search: its actual discovery tree
  (grey lines, in real discovery order - not just a flat visited list) with
  small red dots marking dead-end branches, then the final route traced in
  green. Play/Reset, an adjustable duration, and two pacing modes - "sync"
  (every algorithm finishes together, for comparing routes) or
  "proportional" (slower algorithms visibly take longer, for comparing
  performance).
- Live CPU/memory/time readouts that scale with animation progress
  (estimated by interpolating the measured totals, not re-measured per
  frame - the panel says so).
- A short technical writeup for every algorithm - what it is, how it
  explores, its optimality guarantee (or lack of one), and how it handles
  teleporters - via the "Algorithms" sidebar tab or the `i` button on any
  panel.

Switching mazes or toggling which algorithms are shown keeps your current
selection and playback state instead of resetting them.

## Maze generation styles

Ten structurally different generators, one paired with each benchmark seed
(`BenchmarkStyleNames` in `seeds.go`) so the 10 benchmark mazes stress
pathfinding in ten different ways, not just ten random layouts of the same
algorithm:

| Style           | What makes it different                                                          |
| --------------- | ---------------------------------------------------------------------------------- |
| `Braided`       | The default: a spanning tree with some walls knocked down for loops - multiple valid routes, formally guaranteed (see below). |
| `Perfect`       | No braiding at all - exactly one path between any two cells. Skips teleporters entirely (a teleporter would hand it a second route, breaking the premise). |
| `WideCorridors` | Carved on a half-resolution super-grid then expanded 2x - genuinely 2-cell-wide passages, not the usual 1-cell corridors. |
| `Rooms`         | Classic roguelike layout: a handful of large rectangular rooms, one per slot of a non-overlapping grid layout (so every room is guaranteed to actually get placed, at a genuinely large size), fully open inside, connected by narrow corridors. |
| `SplitHalves`   | Left and right halves are carved as two independent, internally-redundant mazes (braided, with a formal min-routes guarantee, same as `Braided`) with no ordinary wall ever connecting them - the only way across is one mandatory forced teleporter, a deliberate chokepoint that can't be bypassed by choice. Key sits in Start's half, Exit in the other, so reaching it always means actually being forced across. |
| `Prim`          | Randomized Prim's algorithm (frontier-based growth) - many short dead-ends, different texture from DFS. |
| `Kruskal`       | Randomized Kruskal's algorithm (shuffled edges + union-find) - uniformly scattered connections. |
| `Spiral`        | Carved by walking a literal spiral cell order outward-in, plus a few random branch connections. |
| `Cave`          | A spanning tree with heavy braiding (0.55 vs Braided's 0.15) - very open, loop-heavy, "swiss cheese" texture. |
| `Winding`       | Long, mostly-straight strands carved from scattered random starting points across the grid (occasionally drifting 90 degrees rather than turning sharply) - few, long, snake-like corridors with genuine sprawl, not a single walk hugging the grid's boundary. |

Adding a new style: implement it in a new `mazegen_yourname.go` file (see
`mazegen_braided.go` and `mazegen_perfect.go` for the two simplest
examples) and self-register:

```go
func init() {
    RegisterMazeStyle(MazeStyle{Name: "YourStyle", Generate: generateYourStyle})
}

func generateYourStyle(width, height int, seed int64, teleporters int) *Maze {
    m := NewMaze(width, height, seed)
    // ... carve walls via m.carve(cell, dir) only - never touch m.open directly ...
    m.PlacePoints()                    // or m.PlacePointsFrom(start) if Start isn't the grid corner
    m.PlaceTeleporters(teleporters)    // always last - it re-verifies solvability itself
    return m
}
```

Use `m.rng` for all randomness (never `math/rand`'s package-level
functions), so generation stays deterministic per seed. Whatever your style
carves, Start/Key/Exit must end up mutually reachable via ordinary walls
alone before `PlaceTeleporters` runs - it only ever adds teleporters, it
never fixes an already-broken maze.

## Maze rules

- **Walls** carve a perfect (fully-connected) maze via randomized DFS by
  default (the `Braided` style), then extra walls are randomly knocked down
  ("braiding") to create loops. Other styles carve differently - see above.
- **Redundant routes** (`Braided` and `SplitHalves` styles): after braiding,
  the generator verifies - via max-flow / Menger's theorem, not a guess -
  that there are at least `-min-routes` edge-disjoint paths from start to
  key and from key to exit. A corner cell only has 2 possible grid edges,
  so `-min-routes` above 2 is structurally unreachable when start/key/exit
  land on corners; the target is clamped and the program says so rather
  than silently failing to meet it. Each attempt opens a wall next to the
  pair's *current* shortest path rather than picking anywhere in the whole
  grid uniformly at random - a purely random search can end up opening a
  large fraction of the maze's walls before it stumbles onto one that
  actually helps, leaving the maze barely recognizable as one; biasing
  toward the path itself converges in a handful of opens instead.
- **Teleporters** are forced and bidirectional: stepping onto either tile of
  a pair immediately warps you to the other, no choice involved. Rendered
  as a matching uppercase/lowercase letter pair, each teleporter pair its
  own color. Placement is re-checked against a forced-teleport reachability
  simulation so a teleporter can never softlock the maze.
- **Key before exit**: a route must visit the key at some point before its
  final cell is the exit.

## Adding your own algorithm

This is the actual point of the project - implement `Solver` in a new
`solver_yourname.go` file:

```go
package main

func init() {
    Register(&YourSolver{})
}

type YourSolver struct{}

func (YourSolver) Name() string { return "YourName" }

func (YourSolver) Solve(m *Maze) Solution {
    // m.Start, m.Key, m.Exit are the cells you need to connect.
    // m.isOpen(cell, dir), m.neighbor(cell, dir) and m.TeleportLookup()
    // are the primitives every existing solver is built from.
    return Solution{
        Path:    yourRoute,     // Start ... Key ... Exit, inclusive
        Visited: yourVisitedSet, // optional: every cell your search touched, for the viewer's dead-end overlay
        Edges:   yourSearchTree, // optional: []Edge{From, To}, the discovery order your search actually found cells in
    }
}
```

Rules for a valid entry:

- `Solve` must not mutate `m` - the harness times an algorithm by calling
  `Solve` repeatedly against the same maze instance to get a measurement
  finer than a single call's clock resolution, so a mutating `Solve` would
  corrupt its own later runs.
- The returned path is independently re-checked by `ValidatePath`
  (`validator.go`) against the real maze rules before it's trusted, timed,
  or shown - there is no way to fake a shortcut. A path that fails
  validation is disqualified and excluded from the leaderboard, with the
  exact rule it broke printed.
- `Solvers()` runs registered algorithms in alphabetical-by-name order, so
  output is stable regardless of file compilation order.
- `Visited` and `Edges` are both optional - leave them nil if your
  algorithm has no way to report that state. When present, cells on `Path`
  always render on top of both.

## Scoring

Each algorithm's steps, search effort (`ops` - the size of the route's
discovery tree, `len(Solution.Edges)`), allocation count (`allocs` -
`runtime.MemStats.Mallocs` delta), and memory allocated (`mem` -
`TotalAlloc` delta) are each turned into a ratio against whichever
algorithm did *best in that one dimension on that one maze* - so an
algorithm that's merely fast can't out-score one that found a shorter
route just by being fast, and a maze that naturally takes 300 steps to
solve doesn't dominate one that takes 14 just because its raw numbers are
bigger. The score is the geometric mean of those four ratios (1.0 =
matched the best in everything on that maze); in benchmark mode, an
algorithm's final score is the plain average of its per-maze scores across
all 10 mazes.

Deliberately not wall-clock time or CPU time (`diagnostics_windows.go` /
`diagnostics_other.go`) - both are still measured and shown in the viewer
as "does this feel fast" context, but they depend on how busy the machine
running the benchmark happens to be at that exact moment, which makes the
same algorithm on the same maze report a different number every run. Steps,
ops, allocs, and mem are all a pure function of the code path taken: same
maze, same algorithm, same numbers, every time, on any machine, busy or
idle - which is what makes them fair to actually rank on. See
`scoring.go`'s doc comment for the full reasoning.

`span` remains in the exported result as a visualization measure only: the
viewer reveals every edge with the same discovery depth together, then moves
to the next depth. It is not a score input, so parallelism is visible without
making duplicated or speculative concurrent work free.

Current entries: `BFS` (map-based reference implementation, always
optimal), `DFS` (naive baseline, fast but not remotely shortest), `A-Star`
(Manhattan-heuristic best-first search - fast, but teleporters can make the
heuristic inadmissible so it isn't guaranteed optimal here), `Dijkstra`
(priority-queue best-first search with no heuristic at all - the
general-purpose algorithm A-Star specializes; matches BFS's optimal result
here because every move costs the same, but would still be correct on a
maze with non-uniform move costs where BFS's FIFO-order shortcut breaks),
`Claudette` (same BFS algorithm as the reference, rebuilt on flat int32
arrays and a preallocated frontier instead of maps - several times faster,
the standing challenge to beat).

`BrenThread` and `BrenThreadOptimized`: one goroutine per starting
direction, every thread branching into a fresh thread for every legal move
that wins its claim race against the others - no direction preferred over
another, and a thread that loses a claim race isn't wasted: the edge it
found is kept anyway. Once every thread has finished, an ordinary BFS walks
the *complete* graph of every edge any thread ever discovered - not just
the spanning tree of who claimed what first - which is what lets it find
genuine shortcuts a bare claim-tree walk could otherwise lock itself out of
(many threads racing outward with no synchronized notion of "distance so
far" can easily claim a farther cell before a shorter route to it turns up
through a different branch). `BrenThreadOptimized` deliberately does **not**
keep that guarantee - it trades it away for score the same way `A-Star`
trades away BFS's guarantee, just far more aggressively (see below). A
small fixed pool of worker goroutines runs a concurrent best-first search
per leg, pulling whichever claimed cell is currently closest (Manhattan
distance) to that leg's destination off a shared bucket queue - the same
heuristic `A-Star`'s own priority is built on. Claiming is lock-free
(`atomic.CompareAndSwapInt32` on a flat `[]int32`, one claim per cell,
ever), and whichever goroutine wins a cell's claim records itself as that
cell's parent. The instant a leg's destination is itself claimed, that leg
stops - no separate reconstruction pass runs afterward; the route is just
the parent chain walked back to the source, exactly like `A-Star`
reconstructs from its own parent map.

That's what makes it no longer guaranteed-optimal, and unlike `A-Star`'s
fairly small, occasional imprecision (~1.3% extra steps on average across
this project's benchmark seeds), this trade is far more severe -
measured at **~72% of runs suboptimal, with a worst case of well over a
hundred extra steps**. Two things compound beyond plain Manhattan-distance
inadmissibility near teleporters: claims here are permanent the instant
they succeed (no relaxation onto a cheaper predecessor found later, unlike
`A-Star`), and several workers claiming concurrently means cells don't get
claimed in strict best-first order the way a single-threaded heap-pop
would guarantee. This was measured, flagged, and kept this way on purpose:
for this solver, score comes first, route quality second. See
`solver_brenthread_optimized.go`'s doc comment for the full breakdown.

`Hangeon` and `HangeonOptimized` model the maze the way a simple
dungeon-crawler graph library would, rather than the bitmask/direction
style the rest of this codebase uses: a plain adjacency graph, walked by
ordinary breadth-first search. `Hangeon` derives that graph from a
2x-scaled grid with a "gap" vertex on every wall opening (so crossing one
is genuinely two hops, not an abstract "is this direction open" bit),
keyed by a `Coordinate{Row, Col}`-keyed map to fit that sparser structure -
`HangeonOptimized` is the same model built directly at one vertex per
logical cell, skipping the gaps entirely, backed by a flat `[][]int32`
instead of a map since its graph is already dense. `HangeonOptimisedV2` is
the CSR experiment: the same teleport-resolved directed graph represented by
one offset array and one contiguous neighbor array, with a generation-stamped
BFS workspace reused across both legs. In both variants an edge landing on a
teleporter trigger is rewired straight to its paired tile, so traversal stays
oblivious to teleporters.
