package domain

// RawQueryWeight is what the shopper's own words are worth. The server appends them as a probe
// on every search, so base retrieval is this one term and nothing else — there is no second
// implementation of "search without understanding" to keep in step.
const RawQueryWeight = 1.0
