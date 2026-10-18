// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"cmp"
	"slices"
)

// benjaminiHochberg returns the corrected values of a family's p-values,
// in their order: with p(1) <= … <= p(m),
//
//	q(i) = min_{j >= i} min(1, m·p(j)/j)
//
// Tied p-values receive the same q, which the minimum over j >= i gives.
func benjaminiHochberg(p []float64) []float64 {
	m := len(p)
	order := make([]int, m)
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(p[a], p[b]) })
	q := make([]float64, m)
	running := 1.0
	for rank := m; rank >= 1; rank-- {
		i := order[rank-1]
		running = min(running, min(1, float64(float64(m)*p[i])/float64(rank)))
		q[i] = running
	}
	return q
}

// holm returns Holm's step-down adjusted values of the p-values present,
// in their order, over a family of m pairs of which some may have no
// p-value (NaN): such a pair counts in m and is adjusted to nothing. With
// the present p-values sorted ascending, ties kept in their given order,
//
//	h(i) = max_{j <= i} min(1, (m - j + 1)·p(j))
//
// Tied p-values receive the same h, which the maximum over j <= i gives.
func holm(p []float64, present []bool) []float64 {
	m := len(p)
	var order []int
	for i := range p {
		if present[i] {
			order = append(order, i)
		}
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(p[a], p[b]) })
	h := make([]float64, m)
	running := 0.0
	for j, i := range order {
		running = max(running, min(1, float64(float64(m-j)*p[i])))
		h[i] = running
	}
	return h
}
