package audio

// peakAccumulator buckets a mono PCM stream into per-bucket min/max peaks.
type peakAccumulator struct {
	buckets     int
	totalFrames int64
	curBucket   int
	seen        bool
	curMin      float32
	curMax      float32
	min         []float32
	max         []float32
}

func newPeakAccumulator(buckets int, totalFrames int64) *peakAccumulator {
	if totalFrames < 1 {
		totalFrames = 1
	}
	return &peakAccumulator{
		buckets:     buckets,
		totalFrames: totalFrames,
		min:         make([]float32, buckets),
		max:         make([]float32, buckets),
	}
}

// add folds one mono sample (at frameIdx) into its bucket's running min/max.
func (p *peakAccumulator) add(frameIdx int64, mono float32) {
	b := int(frameIdx * int64(p.buckets) / p.totalFrames)
	if b >= p.buckets {
		b = p.buckets - 1
	}
	if b != p.curBucket {
		p.flush()
		p.curBucket = b
		p.seen = false
	}
	if !p.seen {
		p.curMin, p.curMax, p.seen = mono, mono, true
		return
	}
	if mono < p.curMin {
		p.curMin = mono
	}
	if mono > p.curMax {
		p.curMax = mono
	}
}

// flush writes the in-progress bucket's peaks into the result slices.
func (p *peakAccumulator) flush() {
	if !p.seen {
		return
	}
	p.min[p.curBucket] = clampUnit(p.curMin)
	p.max[p.curBucket] = clampUnit(p.curMax)
}

// finish flushes the last bucket and copies results into ps.
func (p *peakAccumulator) finish(ps *PeakSet) {
	p.flush()
	copy(ps.Min, p.min)
	copy(ps.Max, p.max)
}
