// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package objects

import (
	"encoding/json/v2"
	"fmt"
	"testing"
	"time"
)

// BenchmarkJSONVersionAnswerUnmarshal is a stored version read as the API
// answers it: the kind's record decoded, and the id, owner, name and
// digest set beside it. The record is a suite of n tasks and metrics.
func BenchmarkJSONVersionAnswerUnmarshal(b *testing.B) {
	for _, c := range []struct {
		name  string
		tasks int
	}{{"small", 2}, {"medium", 140}, {"large", 2700}} {
		b.Run(c.name, func(b *testing.B) {
			s := Suite{Name: "regression-suite", PrimaryMetric: "sha256:" + fmt.Sprintf("%064x", 1), Weights: map[string]float64{"tests_pass": 0.7, "quality": 0.3}}
			for i := range c.tasks {
				s.Tasks = append(s.Tasks, fmt.Sprintf("sha256:%064x", i+100))
			}
			for i := range 4 {
				s.Metrics = append(s.Metrics, fmt.Sprintf("sha256:%064x", i+1))
			}
			body, err := json.Marshal(s)
			if err != nil {
				b.Fatal(err)
			}
			v := Version{ID: "ver_01JC8M2Q4V7Y", Kind: KindSuite, Owner: "usr_01JC8M2Q4V7Y", Name: s.Name, Digest: fmt.Sprintf("sha256:%064x", 9), Current: true, CreatedAt: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC), Body: body}
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for b.Loop() {
				if _, err := v.Answer(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
