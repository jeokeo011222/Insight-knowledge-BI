package examples

import (
	"math/rand"
	"time"
)

// The sample table mirrors the upstream birth_names shape: one row per
// (month, state, gender, given name) carrying a plausible count. The generator
// is deterministic — a fixed seed means every installation gets identical data,
// which keeps the documentation screenshots reproducible.
func birthNamesRows() [][]any {
	states := []string{"CA", "TX", "FL", "NY", "IL", "PA", "OH", "GA", "NC", "MI"}
	genders := []struct {
		gender string
		names  []string
	}{
		{"boy", []string{
			"James", "Michael", "Robert", "John", "David", "William",
			"Joseph", "Daniel", "Matthew", "Anthony", "Christopher", "Joshua",
		}},
		{"girl", []string{
			"Mary", "Jennifer", "Linda", "Patricia", "Elizabeth", "Susan",
			"Jessica", "Sarah", "Karen", "Nancy", "Lisa", "Ashley",
		}},
	}

	rng := rand.New(rand.NewSource(20180101))
	rows := make([][]any, 0, 12*len(states)*2*12)
	id := 0
	for month := 1; month <= 12; month++ {
		for _, state := range states {
			// A per-state weight keeps the funnel and the heatmap from
			// looking uniform across states.
			weight := 0.6 + rng.Float64()
			for _, g := range genders {
				for _, name := range g.names {
					id++
					day := 1 + rng.Intn(28)
					ds := time.Date(2018, time.Month(month), day, 0, 0, 0, 0, time.UTC)
					num := int(float64(60+rng.Intn(240)) * weight)
					rows = append(rows, []any{id, ds, g.gender, name, num, state})
				}
			}
		}
	}
	return rows
}
