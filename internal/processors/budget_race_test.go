//go:build race

package processors

// budgetSlack widens the wall-clock ceilings in bench_test.go when the race
// detector is on.
//
// Not a fudge factor for noise — a measured property of the instrumentation.
// The race detector instruments every memory access, which is close to the
// entire cost of a tight numeric loop like cordonKNNScores: measured at
// 8.80s under -race against 0.50s without, a 17.5× penalty on the same
// input. A single budget cannot serve both, and the alternatives are worse
// than a multiplier: loosening the plain budget to 20s would stop catching
// anything, and skipping the test under -race would silently drop coverage
// for whoever runs the suite that way.
//
// CI does not currently pass -race (see .github/workflows/ci.yml), so this
// exists for local runs and so that adding -race later doesn't turn a
// correctness gate into a red build.
const budgetSlack = 25
