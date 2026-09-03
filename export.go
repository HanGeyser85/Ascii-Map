package main

import (
	"encoding/json"
	"os"
	"time"
)

// exportFormatVersion bumps whenever the JSON shape changes in a way that
// would break an older copy of viewer.html reading a newer file (or vice
// versa), so the viewer can show a clear "please refresh the page" error
// instead of silently misrendering.
//
// v2: score no longer derives from elapsedNs/cpuNs (see scoring.go for
// why) - opsCount/allocsCount/opsRatio/allocsRatio replace timeRatio/
// cpuRatio. elapsedNs/cpuNs/memBytes are still present and still real
// measurements, just informational now rather than score inputs.
//
// v3: score no longer derives from opsCount either - spanCount/spanRatio
// replace opsRatio (opsCount itself stays, informational: total work
// performed, as opposed to spanCount's critical-path length - see
// scoring.go for why a concurrent solver needs that distinction, not just
// a raw discovery count).
//
// v4: each edge carries an optional Depth (generation number) - see Edge
// in solver.go. Lets a viewer reveal a whole generation's worth of edges
// at once for a solver that reports real values, instead of always one
// edge at a time in array order.
//
// v5: deterministic score uses total ops rather than Span. Span remains
// exported for diagnostics and animation, but a theoretical parallel depth
// does not account for worker startup or synchronization overhead.
const exportFormatVersion = 5

type jsonCell struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type jsonTeleporter struct {
	Label string   `json:"label"`
	From  jsonCell `json:"from"`
	To    jsonCell `json:"to"`
}

// jsonEdge is one step of a search's discovery tree - see Edge and
// Solution.Edges in solver.go.
type jsonEdge struct {
	From  jsonCell `json:"from"`
	To    jsonCell `json:"to"`
	Depth int      `json:"depth,omitempty"` // generation number; 0 = no concurrency info, see Edge in solver.go
}

// jsonResult mirrors runResult plus its score breakdown, in a shape a
// browser can render directly: nanosecond/byte counts instead of
// time.Duration, and the actual visited/path cell lists needed to animate a
// solve rather than just its final ASCII rendering.
type jsonResult struct {
	Algorithm     string     `json:"algorithm"`
	Valid         bool       `json:"valid"`
	InvalidReason string     `json:"invalidReason,omitempty"`
	Steps         int        `json:"steps"`
	Path          []jsonCell `json:"path"`
	Visited       []jsonCell `json:"visited"`
	Edges         []jsonEdge `json:"edges"`
	OpsCount      int        `json:"opsCount"`    // len(Edges); total work done, deterministic score input
	SpanCount     int        `json:"spanCount"`   // critical-path length; informational only
	AllocsCount   int64      `json:"allocsCount"` // heap allocation count; deterministic score input
	MemBytes      int64      `json:"memBytes"`    // bytes allocated; deterministic score input
	ElapsedNs     int64      `json:"elapsedNs"`   // informational only - see scoring.go
	CPUNs         int64      `json:"cpuNs"`       // informational only - see scoring.go
	Score         float64    `json:"score,omitempty"`
	StepsRatio    float64    `json:"stepsRatio,omitempty"`
	OpsRatio      float64    `json:"opsRatio,omitempty"`
	AllocsRatio   float64    `json:"allocsRatio,omitempty"`
	MemRatio      float64    `json:"memRatio,omitempty"`
}

// jsonMaze carries everything needed to redraw a maze from scratch: every
// cell's open-direction bitmask (see directionBits in jsonExport), the
// three landmark cells, teleporter pairs, and every algorithm's result on
// this specific maze.
type jsonMaze struct {
	Seed        int64            `json:"seed"`
	Style       string           `json:"style"`
	Width       int              `json:"width"`
	Height      int              `json:"height"`
	Start       jsonCell         `json:"start"`
	Key         jsonCell         `json:"key"`
	Exit        jsonCell         `json:"exit"`
	Teleporters []jsonTeleporter `json:"teleporters"`
	OpenMask    []int            `json:"openMask"` // row-major, length width*height
	Results     []jsonResult     `json:"results"`
}

type jsonAggregate struct {
	Algorithm    string  `json:"algorithm"`
	AvgScore     float64 `json:"avgScore"`
	AvgSteps     float64 `json:"avgSteps"`
	AvgOps       float64 `json:"avgOps"`  // total work; deterministic score input
	AvgSpan      float64 `json:"avgSpan"` // informational only
	AvgAllocs    float64 `json:"avgAllocs"`
	AvgMemBytes  float64 `json:"avgMemBytes"`
	AvgElapsedNs int64   `json:"avgElapsedNs"` // informational only - see scoring.go
	AvgCPUNs     int64   `json:"avgCpuNs"`     // informational only - see scoring.go
	Wins         int     `json:"wins"`
	Runs         int     `json:"runs"`
}

type jsonExport struct {
	FormatVersion int             `json:"formatVersion"`
	GeneratedAt   string          `json:"generatedAt"`
	DirectionBits jsonDirection   `json:"directionBits"`
	Mazes         []jsonMaze      `json:"mazes"`
	Leaderboard   []jsonAggregate `json:"leaderboard,omitempty"`
}

type jsonDirection struct {
	North int `json:"north"`
	South int `json:"south"`
	East  int `json:"east"`
	West  int `json:"west"`
}

func cellToJSON(c Cell) jsonCell { return jsonCell{X: c.X, Y: c.Y} }

func cellsToJSON(cells []Cell) []jsonCell {
	out := make([]jsonCell, len(cells))
	for i, c := range cells {
		out[i] = cellToJSON(c)
	}
	return out
}

func edgesToJSON(edges []Edge) []jsonEdge {
	out := make([]jsonEdge, len(edges))
	for i, e := range edges {
		out[i] = jsonEdge{From: cellToJSON(e.From), To: cellToJSON(e.To), Depth: e.Depth}
	}
	return out
}

// buildJSONMaze captures a maze's static layout plus every solver's result
// against it (path, visited set, timing, and score breakdown when scored is
// non-nil). results and scored are matched up by algorithm name since
// scoreResults drops disqualified entries and may reorder the rest.
func buildJSONMaze(seed int64, style string, m *Maze, results []runResult, scored []scoredResult) jsonMaze {
	scoreByName := make(map[string]scoredResult, len(scored))
	for _, s := range scored {
		scoreByName[s.name] = s
	}

	teleporters := make([]jsonTeleporter, len(m.Teleporters))
	for i, t := range m.Teleporters {
		teleporters[i] = jsonTeleporter{
			Label: string(t.Label),
			From:  cellToJSON(t.From),
			To:    cellToJSON(t.To),
		}
	}

	openMask := make([]int, m.Width*m.Height)
	for y := 0; y < m.Height; y++ {
		for x := 0; x < m.Width; x++ {
			openMask[y*m.Width+x] = m.CellOpenMask(Cell{X: x, Y: y})
		}
	}

	jsonResults := make([]jsonResult, len(results))
	for i, r := range results {
		jr := jsonResult{
			Algorithm:     r.name,
			Valid:         r.valid,
			InvalidReason: r.invalidReason,
			Steps:         r.steps,
			OpsCount:      r.ops,
			SpanCount:     r.span,
			AllocsCount:   r.allocs,
			MemBytes:      r.memBytes,
			ElapsedNs:     r.elapsed.Nanoseconds(),
			CPUNs:         r.cpuTime.Nanoseconds(),
		}
		if s, ok := scoreByName[r.name]; ok {
			jr.Score = s.score
			jr.StepsRatio = s.stepsRatio
			jr.OpsRatio = s.opsRatio
			jr.AllocsRatio = s.allocsRatio
			jr.MemRatio = s.memRatio
		}
		jsonResults[i] = jr
	}

	return jsonMaze{
		Seed:        seed,
		Style:       style,
		Width:       m.Width,
		Height:      m.Height,
		Start:       cellToJSON(m.Start),
		Key:         cellToJSON(m.Key),
		Exit:        cellToJSON(m.Exit),
		Teleporters: teleporters,
		OpenMask:    openMask,
		Results:     jsonResults,
	}
}

// buildJSONMazeWithSolutions is buildJSONMaze plus the actual per-algorithm
// path/visited cell lists, which runAllSolvers discards after rendering and
// validating (they never lived on runResult, since the console renderer
// only ever needed the final ASCII string). solveAgain re-solves the maze
// once per algorithm purely to recover those cell lists for export - solves
// are cheap (that's the whole premise of the 200x timing loop elsewhere)
// so a single extra pass here is negligible next to the benchmark itself.
func buildJSONMazeWithSolutions(seed int64, style string, m *Maze, results []runResult, scored []scoredResult) jsonMaze {
	jm := buildJSONMaze(seed, style, m, results, scored)
	byName := make(map[string]*jsonResult, len(jm.Results))
	for i := range jm.Results {
		byName[jm.Results[i].Algorithm] = &jm.Results[i]
	}
	for _, solver := range Solvers() {
		jr, ok := byName[solver.Name()]
		if !ok || !jr.Valid {
			continue
		}
		sol := solver.Solve(m.Clone())
		jr.Path = cellsToJSON(sol.Path)
		jr.Visited = cellsToJSON(sol.Visited)
		jr.Edges = edgesToJSON(sol.Edges)
	}
	return jm
}

func buildJSONAggregate(agg []aggregate) []jsonAggregate {
	out := make([]jsonAggregate, len(agg))
	for i, a := range agg {
		out[i] = jsonAggregate{
			Algorithm:    a.name,
			AvgScore:     a.avgScore,
			AvgSteps:     a.avgSteps,
			AvgOps:       a.avgOps,
			AvgSpan:      a.avgSpan,
			AvgAllocs:    a.avgAllocs,
			AvgMemBytes:  a.avgMem,
			AvgElapsedNs: a.avgTime.Nanoseconds(),
			AvgCPUNs:     a.avgCPU.Nanoseconds(),
			Wins:         a.wins,
			Runs:         len(BenchmarkSeeds),
		}
	}
	return out
}

func writeExportJSON(path string, data jsonExport) error {
	data.FormatVersion = exportFormatVersion
	data.GeneratedAt = time.Now().Format(time.RFC3339)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}
