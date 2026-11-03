package speakerid

import (
	"math"
	"testing"
)

func TestCosineSimilarity(t *testing.T) {
	cases := []struct {
		name string
		a, b []float32
		want float64
	}{
		{"same_vector", []float32{1, 0, 0}, []float32{1, 0, 0}, 1.0},
		{"orthogonal", []float32{1, 0, 0}, []float32{0, 1, 0}, 0.0},
		{"opposite", []float32{1, 0, 0}, []float32{-1, 0, 0}, -1.0},
		{"scaled_same_dir", []float32{2, 0, 0}, []float32{5, 0, 0}, 1.0},
		{"forty_five_degrees", []float32{1, 0}, []float32{1, 1}, math.Sqrt2 / 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CosineSimilarity(c.a, c.b)
			if math.Abs(got-c.want) > 1e-6 {
				t.Errorf("CosineSimilarity(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

func TestCosineSimilarity_DegenerateInputs(t *testing.T) {
	cases := []struct {
		name string
		a, b []float32
	}{
		{"length_mismatch", []float32{1, 0, 0}, []float32{1, 0}},
		{"both_empty", []float32{}, []float32{}},
		{"a_all_zero", []float32{0, 0, 0}, []float32{1, 0, 0}},
		{"b_all_zero", []float32{1, 0, 0}, []float32{0, 0, 0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CosineSimilarity(c.a, c.b)
			if !math.IsNaN(got) {
				t.Errorf("CosineSimilarity(%v, %v) = %v, want NaN", c.a, c.b, got)
			}
		})
	}
}

func TestAverageEmbeddings(t *testing.T) {
	t.Run("empty_input_returns_nil", func(t *testing.T) {
		out, err := AverageEmbeddings(nil)
		if err != nil || out != nil {
			t.Errorf("got (%v, %v), want (nil, nil)", out, err)
		}
	})
	t.Run("dim_mismatch_errors", func(t *testing.T) {
		_, err := AverageEmbeddings([][]float32{{1, 0}, {1, 0, 0}})
		if err == nil {
			t.Errorf("expected error on dim mismatch")
		}
	})
	t.Run("single_input_normalized", func(t *testing.T) {
		// Input has norm 2; output should be unit-length.
		in := []float32{2, 0, 0}
		out, err := AverageEmbeddings([][]float32{in})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if math.Abs(float64(out[0])-1.0) > 1e-6 {
			t.Errorf("not normalized: %v", out)
		}
	})
	t.Run("two_aligned_inputs_average_then_normalize", func(t *testing.T) {
		// Two unit vectors along x: mean is (1,0,0), still unit.
		a := []float32{1, 0, 0}
		b := []float32{1, 0, 0}
		out, _ := AverageEmbeddings([][]float32{a, b})
		if math.Abs(float64(out[0])-1.0) > 1e-6 {
			t.Errorf("got %v, want unit on x", out)
		}
	})
	t.Run("two_orthogonal_inputs", func(t *testing.T) {
		// Mean of (1,0) and (0,1) = (0.5, 0.5); normalized = (sqrt2/2, sqrt2/2).
		out, _ := AverageEmbeddings([][]float32{{1, 0}, {0, 1}})
		want := float32(math.Sqrt2 / 2)
		if math.Abs(float64(out[0]-want)) > 1e-6 || math.Abs(float64(out[1]-want)) > 1e-6 {
			t.Errorf("got %v, want (%v, %v)", out, want, want)
		}
	})
	t.Run("opposing_inputs_zero_mean_zero_norm", func(t *testing.T) {
		// (1,0) and (-1,0) average to (0,0); norm is 0; return zeros as-is.
		out, _ := AverageEmbeddings([][]float32{{1, 0}, {-1, 0}})
		if out[0] != 0 || out[1] != 0 {
			t.Errorf("got %v, want zeros", out)
		}
	})
}
