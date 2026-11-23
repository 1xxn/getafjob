package matching

import "math"

// Learning the score weights from the user's own choices (#502, ADR 0029). A logistic regression over the four
// core factors tells how much each of them separates the jobs the user liked from the ones they skipped. Its
// positive coefficients become the weights, blended with the defaults by how many choices there are, so the score
// stays a weighted sum that the reasons explain. Everything is deterministic: same choices, same weights.
const (
	// MinRated is how many rated jobs, and per side, learning needs; below that the defaults stay.
	MinRated        = 20
	MinRatedPerSide = 5
	// MaxRated is how many of the latest choices are read.
	MaxRated = 500
	// blendHalf is the number of choices at which the learned weights count half.
	blendHalf = 50
	// weightFloor keeps every factor in the score.
	weightFloor = 0.05

	learnRounds = 400
	learnRate   = 0.5
	learnL2     = 0.01
)

// Sample is one rated job: its core factors and whether the user liked it.
type Sample struct {
	F     Factors
	Liked bool
}

// LearnWeights returns the user's weights from their samples, or nil when there are too few to learn from.
func LearnWeights(samples []Sample) *Weights {
	liked := 0
	for _, s := range samples {
		if s.Liked {
			liked++
		}
	}
	n := len(samples)
	if n < MinRated || liked < MinRatedPerSide || n-liked < MinRatedPerSide {
		return nil
	}
	// Similarity takes part only when the samples carry it (embedding model on); otherwise it keeps its default.
	withSim := false
	for _, s := range samples {
		if s.F.Similarity != 0 {
			withSim = true
			break
		}
	}
	x := make([][4]float64, n)
	y := make([]float64, n)
	for i, s := range samples {
		x[i] = [4]float64{s.F.Similarity, s.F.Title, s.F.Skills, s.F.Level}
		if s.Liked {
			y[i] = 1
		}
	}
	beta := fit(x, y, withSim)

	def := weightsWithModel
	base := [4]float64{def.Similarity, def.Title, def.Skills, def.Level}
	learned := [4]float64{}
	sum := 0.0
	for k := range beta {
		learned[k] = max(beta[k], 0)
		sum += learned[k]
	}
	if sum <= 0 {
		return nil // nothing separates liked from skipped: the defaults stay
	}
	share := 1.0 // the share the learned factors split among them
	if !withSim {
		share = 1 - def.Similarity
		learned[0] = def.Similarity
	}
	for k := range learned {
		if withSim || k > 0 {
			learned[k] = learned[k] / sum * share
		}
	}
	alpha := float64(n) / float64(n+blendHalf)
	// Every factor keeps the floor; the rest of the score is split in proportion to the blended weights above it.
	var w [4]float64
	above := 0.0
	for k := range w {
		w[k] = max((1-alpha)*base[k]+alpha*learned[k]-weightFloor, 0)
		above += w[k]
	}
	free := 1 - float64(len(w))*weightFloor
	for k := range w {
		w[k] = math.Round((weightFloor+w[k]/above*free)*1000) / 1000
	}
	return &Weights{Similarity: w[0], Title: w[1], Skills: w[2], Level: w[3], Samples: n}
}

// fit runs batch gradient descent on the L2-regularized logistic loss from zero; the intercept is not regularized.
// Factor 0 (similarity) stays at zero when withSim is false.
func fit(x [][4]float64, y []float64, withSim bool) [4]float64 {
	var beta [4]float64
	b0 := 0.0
	n := float64(len(x))
	for range learnRounds {
		var grad [4]float64
		g0 := 0.0
		for i := range x {
			z := b0
			for k := range beta {
				z += beta[k] * x[i][k]
			}
			d := 1/(1+math.Exp(-z)) - y[i]
			g0 += d
			for k := range grad {
				grad[k] += d * x[i][k]
			}
		}
		b0 -= learnRate * g0 / n
		for k := range beta {
			if k == 0 && !withSim {
				continue
			}
			beta[k] -= learnRate * (grad[k]/n + learnL2*beta[k])
		}
	}
	return beta
}
