package domain

import "time"

// NumInterests is how many things a personalised feed will believe an account is into at
// once, and it is the `slot` range of account_interest.
//
// Four because a feed is a page: fewer collapses a browsing history into one taste and shows
// a buyer who saves phones, shoes and houseplants nothing but phones, while more splits the
// same twenty cards so thin that no interest gets enough of them to look like a section.
const NumInterests = 4

// InterestSignals is how far back a recompute reads. A cap rather than a window, because the
// account that saves nothing for a year still has a taste and the one that saves daily does
// not need a thousand rows to describe theirs.
const InterestSignals = 200

// InterestHalfLife is how long a saved listing keeps half its say. What somebody wanted last
// week outranks what they wanted last spring, and without decay a wishlist opened once in
// 2024 pins the feed to it for ever.
const InterestHalfLife = 30 * 24 * time.Hour

// FreshWeight is the share of a personalised feed that is simply what is new here, ranked by
// nothing but its age.
//
// Without it the feed is a closed loop: the interests come from what was saved, what gets
// saved comes from what was shown, and what is shown is the neighbourhood of the interests.
// A taste can then only ever narrow, and a listing posted outside every one of those
// neighbourhoods is unreachable no matter how good it is — which for a marketplace where
// most goods are one-of-a-kind is most of the catalogue. A fifth is enough to keep a door
// open without the page ceasing to be about the reader.
const FreshWeight = 0.2

// FreshPoolFactor is how many candidates each source offers per page of the merge. Bigger
// than one on purpose: the merge samples rather than takes the top, and a pool the size of
// the page leaves it nothing to sample from — four pages deep is where the things a reader
// has not seen live.
const FreshPoolFactor = 4

// ExploreSharpness is how hard the draw leans on rank. The draw gives a listing at rank r a
// weight of 1/r^this, so raising it concentrates the odds at the top of each source: at 1 the
// feed opened on a middling match about as often as on a good one, which reads as a broken
// ranking rather than as discovery. At 2 the best match of a source takes roughly three in
// five of its turns and the tail still comes up.
//
// Applied to the rank and never to the weight, or the shares stop being shares: squaring an
// interest worth a tenth of the signal against one worth nine tenths would give it a
// hundredth of the page instead of a tenth, and the point of merging sources rather than
// scoring against all of them was that the small one still reaches the reader.
const ExploreSharpness = 2

// InterestMaxDistance is how far an interest leg will reach before it would rather return
// nothing. Cosine distance, so 0 is the same vector and 1 is unrelated.
//
// A slot's vector is `avg(dense)` over the listings an account touched in one category, and the
// mean of a diverse group is a point that no product occupies. Measured on a real account: two
// healthy slots whose nearest listing sat at 0.052 and 0.000, and two whose nearest sat at 0.385
// and 0.417 — the second of those was labelled "Trang trí nhà cửa & Đèn chiếu sáng" and filled
// with dog bowls, because the label comes from the nearest *category* and the cards from the
// nearest *listings*, and out in empty space those two stop agreeing.
//
// Without a floor the leg still returns @candidates rows, however far away they are: `ORDER BY
// … LIMIT` has no notion of far. This is the same guard `search`'s ANN legs get from leg_floor,
// which is relative to the best hit; here it is absolute, because there is no query to be
// relative to.
const InterestMaxDistance = 0.30

// OutOfStockPenalty is subtracted from a recommendation's score when nothing on the listing can
// still be bought — no live variant with quantity above what is reserved and sold.
//
// Subtracted and not excluded, because a listing out of stock today is still the best answer to
// what somebody likes, and a buyer who saves it or asks the seller is better served than one
// shown a worse match. It is 0.25 because that is about the spread of a healthy interest leg:
// scores there run from ~0.95 down to 0.70 (the floor is a distance of 0.30), so a penalty of a
// quarter drops a row below the in-stock rows near it while still keeping it above genuinely
// distant ones.
//
// The effect on the page is larger than the number suggests, and deliberately so: the draw
// weights a row by 1/rank^ExploreSharpness, so a row pushed from third to thirtieth is about a
// hundred times less likely to be shown. Measured on the catalogue this was written against,
// 34.6% of active listings had nothing available.
const OutOfStockPenalty = 0.25

// SeedRotation is how often a feed reshuffles for a caller who names no seed of their own.
// A client that pages through the feed sends one seed for the whole run, since the ordering
// has to hold still under it; this is the fallback that keeps everyone else from reading the
// same page for ever. Short on purpose — a minute, not fifteen — because the fallback is what a
// demo or a bare curl against the API sees, and the wait to prove the feed moves at all should
// not be longer than the demo itself.
const SeedRotation = time.Minute
