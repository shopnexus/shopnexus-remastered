package domain

// RawQueryWeight is what the shopper's own words are worth. The server appends them as a probe
// on every search, so base retrieval is this one term and nothing else — there is no second
// implementation of "search without understanding" to keep in step.
const RawQueryWeight = 1.0

// RRFConstant is the `k` of reciprocal rank fusion. 60 is the value the method was published
// with, and it is not a tuning knob here: it sets how fast a rank's contribution decays, and
// every weight below was chosen against it.
const RRFConstant = 60.0

// LegRelevanceFloor is the share of a leg's best score a hit must reach to survive into the
// fusion. Measured on this catalogue against cosine scores: the genuine hits for a narrow query
// sit at 0.93–1.00 of the best and the first unrelated one at 0.47, while a broad query stays
// above 0.71 all the way down — so 0.6 falls in the gap for both shapes. Applied per leg,
// because that is the only place two scores came out of the same operator.
const LegRelevanceFloor = 0.6

// DenseShare and SparseShare split one probe's weight between meaning and words. Equal to start
// with, and that is a guess: nothing has measured which half this catalogue's queries need more
// of. A missing half contributes nothing rather than handing its share to the other one — less
// evidence is less signal, not the same signal from one side.
const (
	DenseShare  = 0.5
	SparseShare = 0.5
)
