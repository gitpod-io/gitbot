package bot

import (
	"fmt"
	"math"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseEffortEstimate(t *testing.T) {
	type Expectation struct {
		Error    string
		Estimate *EffortEstimate
	}
	var (
		onePtwo float64 = 1.2
		five    float64 = 5
		ten     float64 = 10
	)
	tests := []struct {
		Input       string
		Expectation Expectation
	}{
		{
			Input: "/effort 5",
			Expectation: Expectation{
				Estimate: &EffortEstimate{
					Med: &five,
				},
			},
		},
		{
			Input: "/effort 5\nmin 10",
			Expectation: Expectation{
				Estimate: &EffortEstimate{
					Min: &ten,
					Med: &five,
				},
			},
		},
		{
			Input: "/effort 5\nmax 10",
			Expectation: Expectation{
				Estimate: &EffortEstimate{
					Med: &five,
					Max: &ten,
				},
			},
		},
		{
			Input: "/effort 5\nabc hello\nworld\nmin 1.2",
			Expectation: Expectation{
				Estimate: &EffortEstimate{
					Min: &onePtwo,
					Med: &five,
				},
			},
		},
	}

	for i, test := range tests {
		t.Run(fmt.Sprintf("%03d", i), func(t *testing.T) {
			est, err := parseEffortEstimate(test.Input)
			var act Expectation
			if err != nil {
				act.Error = err.Error()
			} else {
				act.Estimate = est
			}

			opt := cmp.Comparer(func(x, y *float64) bool {
				if x == nil && y == nil {
					return true
				}
				if x == nil || y == nil {
					return false
				}
				delta := math.Abs(*x - *y)
				return delta < 0.00001
			})

			if diff := cmp.Diff(test.Expectation, act, opt); diff != "" {
				t.Errorf("parseEffortEstimate() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
