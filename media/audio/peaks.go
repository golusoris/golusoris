// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package audio

import "math/bits"

type peakCollector interface {
	add(frameIdx int64, mono float32)
	finish(ps *PeakSet)
}

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

// streamingPeakAccumulator retains at most two summaries per output bucket.
// Full summary sets compact adjacent time ranges before more samples arrive.
type streamingPeakAccumulator struct {
	buckets     int
	maxBins     int
	binFrames   int64
	totalFrames int64
	current     peakSummary
	bins        []peakSummary
}

type peakSummary struct {
	frames int64
	min    float32
	max    float32
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

func newStreamingPeakAccumulator(buckets int) *streamingPeakAccumulator {
	if buckets < 1 {
		buckets = 1
	}
	maxBins := buckets * 2
	return &streamingPeakAccumulator{
		buckets:   buckets,
		maxBins:   maxBins,
		binFrames: 1,
		bins:      make([]peakSummary, 0, maxBins),
	}
}

// add folds one mono sample (at frameIdx) into its bucket's running min/max.
func (p *peakAccumulator) add(frameIdx int64, mono float32) {
	b := peakBucketIndex(frameIdx, p.buckets, p.totalFrames)
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

func peakBucketIndex(frameIdx int64, buckets int, totalFrames int64) int {
	if frameIdx <= 0 || buckets < 1 || totalFrames < 1 {
		return 0
	}
	if frameIdx >= totalFrames {
		return buckets - 1
	}
	high, low := bits.Mul64(uint64(frameIdx), uint64(buckets))
	index, _ := bits.Div64(high, low, uint64(totalFrames))
	if index >= uint64(buckets) {
		return buckets - 1
	}
	// #nosec G115 -- index is proven below the caller-supplied int bucket count.
	return int(index)
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

func (p *streamingPeakAccumulator) add(_ int64, mono float32) {
	if p.current.frames == 0 {
		p.current.min, p.current.max = mono, mono
	} else {
		p.current.min = min(p.current.min, mono)
		p.current.max = max(p.current.max, mono)
	}
	p.current.frames++
	p.totalFrames++
	if p.current.frames == p.binFrames {
		p.appendCurrent()
	}
}

func (p *streamingPeakAccumulator) appendCurrent() {
	if p.current.frames == 0 {
		return
	}
	p.bins = append(p.bins, p.current)
	p.current = peakSummary{}
	if len(p.bins) == p.maxBins {
		p.compact()
	}
}

func (p *streamingPeakAccumulator) compact() {
	write := 0
	for read := 0; read+1 < len(p.bins); read += 2 {
		left, right := p.bins[read], p.bins[read+1]
		p.bins[write] = peakSummary{
			frames: left.frames + right.frames,
			min:    min(left.min, right.min),
			max:    max(left.max, right.max),
		}
		write++
	}
	p.bins = p.bins[:write]
	const maxInt64 = int64(^uint64(0) >> 1)
	if p.binFrames <= maxInt64/2 {
		p.binFrames *= 2
	} else {
		p.binFrames = maxInt64
	}
}

func (p *streamingPeakAccumulator) finish(ps *PeakSet) {
	p.appendCurrent()
	if p.totalFrames == 0 {
		return
	}
	seen := make([]bool, p.buckets)
	var firstFrame int64
	for _, summary := range p.bins {
		lastFrame := firstFrame + summary.frames - 1
		firstBucket := peakBucketIndex(firstFrame, p.buckets, p.totalFrames)
		lastBucket := peakBucketIndex(lastFrame, p.buckets, p.totalFrames)
		for bucket := firstBucket; bucket <= lastBucket; bucket++ {
			mergePeakSummary(ps, seen, bucket, summary)
		}
		firstFrame += summary.frames
	}
}

func mergePeakSummary(ps *PeakSet, seen []bool, bucket int, summary peakSummary) {
	minimum, maximum := clampUnit(summary.min), clampUnit(summary.max)
	if !seen[bucket] {
		ps.Min[bucket], ps.Max[bucket], seen[bucket] = minimum, maximum, true
		return
	}
	ps.Min[bucket] = min(ps.Min[bucket], minimum)
	ps.Max[bucket] = max(ps.Max[bucket], maximum)
}
