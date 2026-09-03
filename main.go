package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"
)

type runResult struct {
	name     string
	elapsed  time.Duration // informational only - see scoring.go's doc comment for why this no longer feeds the score
	cpuTime  time.Duration // informational only - see scoring.go's doc comment for why this no longer feeds the score
	memBytes int64
	allocs   int64 // heap allocation count (runtime.MemStats.Mallocs delta) - deterministic score input
	ops      int   // search effort: len(Solution.Edges) - deterministic score input
	span     int   // critical-path length: Solution.Span, or ops when unset; informational only
	steps    int   // moves taken (path cell count - 1)

	valid         bool
	invalidReason string
	rendered      string
}

// mapSizePreset keeps the common run sizes named and repeatable. The
// xlarge preset deliberately has enough cells to expose scaling problems in
// a solver or its bookkeeping; use it with -summary-only or a focused style
// while iterating, since the harness runs every registered solver.
func mapSizePreset(name string) (width, height int, ok bool) {
	switch strings.ToLower(name) {
	case "normal":
		return 21, 15, true
	case "large":
		return 100, 100, true
	case "xlarge", "x-large":
		return 250, 250, true
	default:
		return 0, 0, false
	}
}

func main() {
	enableANSI()

	width := flag.Int("width", 21, "maze width in cells")
	height := flag.Int("height", 15, "maze height in cells")
	size := flag.String("size", "normal", "maze-size preset: normal (21x15), large (100x100), or xlarge (250x250)")
	seed := flag.Int64("seed", 42, "random seed (same seed -> same maze)")
	teleporters := flag.Int("teleporters", 2, "number of teleporter pairs")
	braid := flag.Float64("braid", 0.15, "probability of opening an extra wall to create loops (0-1)")
	minPaths := flag.Int("min-routes", 2, "minimum edge-disjoint routes required to the key and to the exit")
	bench := flag.Bool("bench", false, "run every algorithm against all 10 hardcoded benchmark seeds and print a leaderboard")
	summaryOnly := flag.Bool("summary-only", false, "skip printing each solved map; show only the leaderboard(s)")
	noColor := flag.Bool("no-color", false, "disable ANSI color output")
	consoleWidthFlag := flag.Int("console-width", 0, "override detected console width for side-by-side maze panels (0 = auto)")
	style := flag.String("style", "Braided", "maze generation style (-list-styles to see all); -braid/-min-routes only apply to Braided")
	listStyles := flag.Bool("list-styles", false, "print every registered maze style and exit")
	consoleRoutes := flag.Bool("console-routes", false, "also dump every solved maze as ASCII panels to the console (default: write JSON for viewer.html instead)")
	outPath := flag.String("out", "", "path to write the JSON results file (default: bench_results.json for -bench, maze_result.json otherwise)")
	flag.Parse()

	if *noColor {
		colorsEnabled = false
	}

	if *listStyles {
		for _, s := range MazeStyles() {
			fmt.Println(s.Name)
		}
		return
	}

	if *bench {
		runBenchmark(*summaryOnly, *consoleWidthFlag, *consoleRoutes, *outPath)
		return
	}

	presetWidth, presetHeight, ok := mapSizePreset(*size)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown size preset %q; choose normal, large, or xlarge\n", *size)
		os.Exit(1)
	}
	widthSpecified, heightSpecified := false, false
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "width":
			widthSpecified = true
		case "height":
			heightSpecified = true
		}
	})
	// A named size selects both dimensions, but callers can tune either
	// axis explicitly: -size xlarge -width 400, for example, is 400x250.
	if !widthSpecified {
		*width = presetWidth
	}
	if !heightSpecified {
		*height = presetHeight
	}

	if *width < 5 || *height < 5 {
		fmt.Fprintln(os.Stderr, "width and height must be at least 5")
		os.Exit(1)
	}
	if *minPaths < 1 {
		fmt.Fprintln(os.Stderr, "min-routes must be at least 1")
		os.Exit(1)
	}

	var m *Maze
	if *style == "Braided" {
		m = buildMaze(*width, *height, *seed, *teleporters, *braid, *minPaths)
	} else {
		s, ok := mazeStyleByName(*style)
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown style %q; run -list-styles to see the available ones\n", *style)
			os.Exit(1)
		}
		m = s.Generate(*width, *height, *seed, *teleporters)
	}
	fmt.Printf("seed=%d  style=%s  size=%dx%d\n\n", *seed, *style, *width, *height)
	fmt.Print(m.Render())
	fmt.Println()
	fmt.Print(m.Legend())
	fmt.Println()

	results := runAllSolvers(m)
	scored := scoreResults(results)
	printLeaderboard("Leaderboard", results)

	if *consoleRoutes {
		printPanelsOrSummary(results, *summaryOnly, resolveConsoleWidth(*consoleWidthFlag))
	}

	path := *outPath
	if path == "" {
		path = "maze_result.json"
	}
	jm := buildJSONMazeWithSolutions(*seed, *style, m, results, scored)
	export := jsonExport{
		DirectionBits: jsonDirection{North: North, South: South, East: East, West: West},
		Mazes:         []jsonMaze{jm},
	}
	if err := writeExportJSON(path, export); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write %s: %v\n", path, err)
	} else {
		fmt.Printf("Full results + animated route data written to %s - open viewer.html in a browser to explore.\n", path)
	}
}

// buildMaze runs the full generation pipeline: carve, braid, place start/
// key/exit, guarantee redundant routes, then place teleporters without
// breaking solvability.
func buildMaze(width, height int, seed int64, teleporters int, braid float64, minPaths int) *Maze {
	m := NewMaze(width, height, seed)
	m.GeneratePerfectMaze()
	m.Braid(braid)
	m.PlacePoints()
	m.EnsureRedundantRoutes(m.Start, m.Key, m.Exit, minPaths)
	m.PlaceTeleporters(teleporters)
	return m
}

// timingRuns is how many times each algorithm re-solves the same maze to
// produce its reported time and CPU time. A single Solve() call on a small
// maze routinely finishes faster than the OS clock can reliably resolve (it
// reads as a flat 0s), which defeats the entire point of a speed
// leaderboard - averaging over many repeats gives a real, comparable
// number instead. These stay purely informational now (see scoring.go) -
// they're still worth reporting since they're the closest thing to "how
// this would actually feel," but a shared machine's own business (whatever
// else is competing for the CPU that moment) can and does swing them run to
// run, which is exactly what makes them unfit to decide rank.
const timingRuns = 200

// runAllSolvers hands every registered algorithm its own untouched clone of
// the maze, measures it, and independently validates what comes back before
// trusting it. Two different kinds of measurement happen here, and they're
// used for two different purposes:
//
//   - allocs and memBytes (runtime.MemStats deltas around one Solve() call)
//     and ops (len(Solution.Edges), search effort) are a pure function of
//     the code path taken for this exact maze - same maze, same algorithm,
//     same numbers every time, on any machine, busy or idle. These are what
//     scoreResults actually scores on.
//   - elapsed and cpuTime (averaged over many repeats, to get above the
//     OS clock's resolution floor) reflect real wall-clock/CPU behavior,
//     which is exactly what makes them useful as a "does this feel fast"
//     sanity check and exactly what makes them unsuitable to rank on: how
//     busy the machine happens to be at that moment shows up directly in
//     the number.
func runAllSolvers(m *Maze) []runResult {
	var results []runResult
	for _, solver := range Solvers() {
		clone := m.Clone()

		// One call to capture the actual answer to validate/render - also
		// used to measure allocs/memBytes, since runtime.ReadMemStats has
		// its own non-trivial overhead unsuitable for the tight timing
		// loop below, and since a single call's allocation profile is
		// already exactly reproducible (no averaging needed for a
		// deterministic quantity).
		var memBefore, memAfter runtime.MemStats
		runtime.ReadMemStats(&memBefore)
		sol := solver.Solve(clone)
		runtime.ReadMemStats(&memAfter)
		memBytes := int64(memAfter.TotalAlloc - memBefore.TotalAlloc)
		allocs := int64(memAfter.Mallocs - memBefore.Mallocs)

		// Repeat runs, reusing the same clone (all shipped solvers are
		// read-only over the maze), purely to get wall-clock and CPU-time
		// measurements finer than a single call's resolution. Informational
		// only - see this function's doc comment.
		cpuBefore := processCPUTime()
		start := time.Now()
		for i := 0; i < timingRuns; i++ {
			solver.Solve(clone)
		}
		elapsed := time.Since(start) / timingRuns
		cpuElapsed := (processCPUTime() - cpuBefore) / time.Duration(timingRuns)

		ops := len(sol.Edges)
		// Span remains useful for visualization and diagnostics, but it is
		// deliberately not a score input: theoretical dependency depth does
		// not include a concurrent solver's real worker/synchronization cost.
		span := sol.Span
		if span == 0 {
			span = ops
		}

		res := runResult{
			name:     solver.Name(),
			elapsed:  elapsed,
			cpuTime:  cpuElapsed,
			memBytes: memBytes,
			allocs:   allocs,
			ops:      ops,
			span:     span,
			steps:    len(sol.Path) - 1,
		}
		if err := ValidatePath(m, sol.Path); err != nil {
			res.valid = false
			res.invalidReason = err.Error()
		} else {
			res.valid = true
			res.rendered = m.RenderPath(sol.Path, sol.Visited)
		}
		results = append(results, res)
	}
	return results
}

// formatBytes renders a byte count in whatever unit reads most naturally.
func formatBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.2fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.2fKB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// printPanelsOrSummary shows each algorithm's result either as a plain text
// line (summaryOnly) or as a solved-maze panel, packed side by side with
// its peers to use the console's width instead of dumping every maze in
// one long vertical column.
func printPanelsOrSummary(results []runResult, summaryOnly bool, consoleWidth int) {
	if summaryOnly {
		for _, r := range results {
			if !r.valid {
				fmt.Printf("%-20s DISQUALIFIED: %s\n", r.name, r.invalidReason)
				continue
			}
			fmt.Printf("%-20s steps: %-6d time: %-14s cpu: %-14s mem: %s\n",
				r.name, r.steps, r.elapsed, r.cpuTime, formatBytes(r.memBytes))
		}
		fmt.Println()
		return
	}

	panels := make([]panel, len(results))
	for i, r := range results {
		panels[i] = buildPanel(r)
	}
	printPanelsGrid(panels, consoleWidth)
}

// printLeaderboard ranks algorithms by combined score (see scoring.go) and
// calls out the run's shortest route and fastest solve up front, so those
// headline numbers are visible even if the per-algorithm output above has
// scrolled off.
func printLeaderboard(title string, results []runResult) {
	scored := scoreResults(results)

	fmt.Printf("--- %s (deterministic efficiency score = geometric mean of steps/ops/allocs/mem ratios vs. the best on this maze; 1.0 = best in everything, smallest first; span/time/cpu are informational only - see scoring.go) ---\n", title)

	if len(scored) > 0 {
		minSteps := scored[0].steps
		minElapsed := scored[0].elapsed
		for _, r := range scored {
			if r.steps < minSteps {
				minSteps = r.steps
			}
			if r.elapsed < minElapsed {
				minElapsed = r.elapsed
			}
		}
		var shortestBy, fastestBy []string
		for _, r := range scored {
			if r.steps == minSteps {
				shortestBy = append(shortestBy, r.name)
			}
			if r.elapsed == minElapsed {
				fastestBy = append(fastestBy, r.name)
			}
		}
		fmt.Printf("Shortest route this run: %d steps (%s)\n", minSteps, strings.Join(shortestBy, ", "))
		fmt.Printf("Fastest solve this run (informational, not scored): %s (%s)\n\n", minElapsed, strings.Join(fastestBy, ", "))
	}

	for i, r := range scored {
		fmt.Printf("%2d. %-20s score %6.2f   %6d steps   %6d ops   %6d allocs   mem %10s   (span %d, time %s, cpu %s)\n",
			i+1, r.name, r.score, r.steps, r.ops, r.allocs, formatBytes(r.memBytes), r.span, r.elapsed, r.cpuTime)
	}
	disqualified := len(results) - len(scored)
	if disqualified > 0 {
		fmt.Printf("(%d algorithm(s) disqualified - see output above)\n", disqualified)
	}
	fmt.Println()
}

type seedRun struct {
	seed    int64
	style   string
	maze    *Maze
	results []runResult
}

type aggregate struct {
	name      string
	avgScore  float64
	avgSteps  float64
	avgOps    float64 // total work; deterministic score input
	avgSpan   float64 // informational only
	avgAllocs float64
	avgMem    float64
	avgTime   time.Duration // informational only - see scoring.go
	avgCPU    time.Duration // informational only - see scoring.go
	wins      int
}

// runBenchmark regenerates the same 10 fixed mazes every time (so results
// are repeatable across runs and machines) and runs every algorithm against
// each one, printing progress as it goes since generating+solving all 10
// can take a few seconds and a silently blank console reads as frozen. The
// summary (every algorithm, ranked by average score across all 10 seeds) is
// printed first; the route visualizer - one row per algorithm, that
// algorithm's solved maze for all 10 seeds side by side - follows below it.
func runBenchmark(summaryOnly bool, consoleWidthOverride int, consoleRoutes bool, outPath string) {
	consoleWidth := resolveConsoleWidth(consoleWidthOverride)

	var runsBySeed []seedRun
	for i, seed := range BenchmarkSeeds {
		styleName := BenchmarkStyleNames[i]
		style, ok := mazeStyleByName(styleName)
		if !ok {
			fmt.Fprintf(os.Stderr, "internal error: benchmark style %q is not registered\n", styleName)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "[%d/%d] %s (seed=%d): generating maze...\n", i+1, len(BenchmarkSeeds), styleName, seed)
		m := style.Generate(BenchWidth, BenchHeight, seed, BenchTeleporters)
		fmt.Fprintf(os.Stderr, "[%d/%d] %s (seed=%d): running %d algorithms...\n", i+1, len(BenchmarkSeeds), styleName, seed, len(Solvers()))
		runsBySeed = append(runsBySeed, seedRun{seed: seed, style: styleName, maze: m, results: runAllSolvers(m)})
	}
	fmt.Fprintln(os.Stderr, "done.")
	fmt.Fprintln(os.Stderr)

	agg := aggregateScores(runsBySeed)

	fmt.Println("=== All algorithms - deterministic efficiency score across all 10 seeds (geometric mean of steps/ops/allocs/mem ratios vs. the best on each maze; smallest first; span/time/cpu below are informational only - see scoring.go) ===")
	for i, a := range agg {
		fmt.Printf("%2d. %-20s avg score %6.2f   avg %6.1f steps   avg %7.1f ops   avg %7.1f allocs   avg mem %10s   won %d/%d   (avg span %.1f, avg time %s, avg cpu %s)\n",
			i+1, a.name, a.avgScore, a.avgSteps, a.avgOps, a.avgAllocs, formatBytes(int64(a.avgMem)), a.wins, len(BenchmarkSeeds), a.avgSpan, a.avgTime, a.avgCPU)
	}
	fmt.Println()

	// Every algorithm's path/visited cells against every one of the 10
	// mazes, plus the layout needed to redraw each maze - this is what lets
	// viewer.html animate solves and compare algorithms side by side,
	// instead of the console dumping ten mazes times N algorithms of ASCII
	// art that scrolls past faster than anyone can read it.
	fmt.Fprintln(os.Stderr, "building export (re-solving each algorithm once per maze to capture path/visited data)...")
	jsonMazes := make([]jsonMaze, len(runsBySeed))
	for i, sr := range runsBySeed {
		scored := scoreResults(sr.results)
		jsonMazes[i] = buildJSONMazeWithSolutions(sr.seed, sr.style, sr.maze, sr.results, scored)
	}
	path := outPath
	if path == "" {
		path = "bench_results.json"
	}
	export := jsonExport{
		DirectionBits: jsonDirection{North: North, South: South, East: East, West: West},
		Mazes:         jsonMazes,
		Leaderboard:   buildJSONAggregate(agg),
	}
	if err := writeExportJSON(path, export); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write %s: %v\n", path, err)
	} else {
		fmt.Printf("Full results + animated route data for all %d mazes written to %s - open viewer.html in a browser to explore (compare algorithms side by side, animate solves, see cpu/mem).\n\n", len(runsBySeed), path)
	}

	if summaryOnly || !consoleRoutes {
		return
	}

	fmt.Println("=== Route visualizer - each algorithm's solved map for all 10 seeds ===")
	fmt.Println()
	for _, a := range agg {
		fmt.Printf("--- %s ---\n", a.name)
		var panels []panel
		for _, sr := range runsBySeed {
			var res runResult
			for _, r := range sr.results {
				if r.name == a.name {
					res = r
					break
				}
			}
			p := buildPanel(res)
			p.title = fmt.Sprintf("seed=%d [%s]: %s", sr.seed, sr.style, p.title)
			panels = append(panels, p)
		}
		printPanelsGrid(panels, consoleWidth)
	}
}

// aggregateScores scores every seed's results (see scoreResults in
// scoring.go), averages each algorithm's score/steps/ops/allocs/mem (the
// deterministic quantities scored on) plus span/time/cpu (informational
// only) across all seeds it was valid in, and returns them sorted
// best-average-score first. A seed's top scorer counts as a "win" for
// that seed.
func aggregateScores(runsBySeed []seedRun) []aggregate {
	totalScore := map[string]float64{}
	totalSteps := map[string]int{}
	totalOps := map[string]int{}
	totalSpan := map[string]int{}
	totalAllocs := map[string]int64{}
	totalMem := map[string]int64{}
	totalTime := map[string]time.Duration{}
	totalCPU := map[string]time.Duration{}
	wins := map[string]int{}
	runs := map[string]int{}

	for _, sr := range runsBySeed {
		scored := scoreResults(sr.results)
		for i, r := range scored {
			totalScore[r.name] += r.score
			totalSteps[r.name] += r.steps
			totalOps[r.name] += r.ops
			totalSpan[r.name] += r.span
			totalAllocs[r.name] += r.allocs
			totalMem[r.name] += r.memBytes
			totalTime[r.name] += r.elapsed
			totalCPU[r.name] += r.cpuTime
			runs[r.name]++
			if i == 0 {
				wins[r.name]++ // best combined score for this seed
			}
		}
	}

	var agg []aggregate
	for name, n := range runs {
		if n == 0 {
			continue
		}
		agg = append(agg, aggregate{
			name:      name,
			avgScore:  totalScore[name] / float64(n),
			avgSteps:  float64(totalSteps[name]) / float64(n),
			avgOps:    float64(totalOps[name]) / float64(n),
			avgSpan:   float64(totalSpan[name]) / float64(n),
			avgAllocs: float64(totalAllocs[name]) / float64(n),
			avgMem:    float64(totalMem[name]) / float64(n),
			avgTime:   totalTime[name] / time.Duration(n),
			avgCPU:    totalCPU[name] / time.Duration(n),
			wins:      wins[name],
		})
	}
	sort.Slice(agg, func(i, j int) bool { return agg[i].avgScore < agg[j].avgScore })
	return agg
}
