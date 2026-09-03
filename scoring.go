package main

import (
	"math"
	"sort"
)

// scoredResult is the deterministic-efficiency score. It compares route
// quality, total search work, allocation count, and allocation volume. It
// intentionally uses ops rather than Span: total work is comparable whether
// it was performed serially or by many workers, while Span is only a model of
// ideal parallel dependency and omits real scheduling/synchronization cost.
type scoredResult struct {
	runResult
	stepsRatio  float64
	opsRatio    float64
	allocsRatio float64
	memRatio    float64
	score       float64
}

func validResults(results []runResult) []runResult {
	valid := make([]runResult, 0, len(results))
	for _, r := range results {
		if r.valid {
			valid = append(valid, r)
		}
	}
	return valid
}

func atLeastOne(n int64) int64 {
	if n < 1 {
		return 1
	}
	return n
}

// scoreResults ranks deterministic efficiency using the geometric mean of
// normalized steps, ops, allocations, and allocated bytes. Every dimension
// is compared only against the best valid solver on the same maze.
func scoreResults(results []runResult) []scoredResult {
	valid := validResults(results)
	if len(valid) == 0 {
		return nil
	}

	bestSteps, bestOps := valid[0].steps, valid[0].ops
	bestAllocs, bestMem := valid[0].allocs, valid[0].memBytes
	for _, r := range valid[1:] {
		if r.steps < bestSteps {
			bestSteps = r.steps
		}
		if r.ops < bestOps {
			bestOps = r.ops
		}
		if r.allocs < bestAllocs {
			bestAllocs = r.allocs
		}
		if r.memBytes < bestMem {
			bestMem = r.memBytes
		}
	}
	bestSteps = int(atLeastOne(int64(bestSteps)))
	bestOps = int(atLeastOne(int64(bestOps)))
	bestAllocs = atLeastOne(bestAllocs)
	bestMem = atLeastOne(bestMem)

	scored := make([]scoredResult, len(valid))
	for i, r := range valid {
		steps := int(atLeastOne(int64(r.steps)))
		ops := int(atLeastOne(int64(r.ops)))
		allocs := atLeastOne(r.allocs)
		mem := atLeastOne(r.memBytes)
		stepsRatio := float64(steps) / float64(bestSteps)
		opsRatio := float64(ops) / float64(bestOps)
		allocsRatio := float64(allocs) / float64(bestAllocs)
		memRatio := float64(mem) / float64(bestMem)
		scored[i] = scoredResult{
			runResult:   r,
			stepsRatio:  stepsRatio,
			opsRatio:    opsRatio,
			allocsRatio: allocsRatio,
			memRatio:    memRatio,
			score:       math.Sqrt(math.Sqrt(stepsRatio * opsRatio * allocsRatio * memRatio)),
		}
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score < scored[j].score
		}
		return scored[i].ops < scored[j].ops
	})
	return scored
}
