//go:build !race

package processors

// budgetSlack is 1 in an ordinary run: the budgets in bench_test.go are
// stated for uninstrumented code. See budget_race_test.go for why the race
// build needs a multiplier.
const budgetSlack = 1
