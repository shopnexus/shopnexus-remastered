# bulkseed — a catalogue with volume, spread evenly

`cmd/seed` writes the demo: a hundred listings with an invented history behind each one —
orders in every state, negotiations, reviews with replies. This writes **volume**. Nothing
here has a history; every row is a listing and nothing else, because the question it answers
is whether a browse feed, a category page and a paginator hold up over a hundred thousand
rows spread evenly over every leaf.

The two are not alternatives. Run `cmd/seed` for something to look at, run this for something
to load.

## Why it exists

The live catalogue had grown lopsided: 319 listings in `Thời trang & Quần áo`, none at all in
`Đồng hồ`, `Sân vườn & Ngoại thất` or `Công nghiệp & Thương mại`, and two overlapping sets of
categories saying the same thing twice (`Laptop & Máy tính` beside `Máy tính & Thiết bị mạng`).
A category page cannot be judged on three listings and a paginator cannot be judged on one page.

## The parts

One entry point. Everything is a subcommand of `./seed`, because the failure mode of a folder
of numbered scripts is not that one of them breaks — it is that somebody runs 03 before 02 on a
new machine and finds out an hour later.

```
./seed status                    what exists already
./seed all --total 1000000       every step, skipping whatever is done
./seed verify                    check the claims this tool makes about its output
```

| Path | What it is |
|---|---|
| `seed` | the entry point: status, categories, crawl, photos, load, embed, verify, wipe, all |
| `lib/leafmap.py` | our 34 catalogue leaves, and which Tiki categories feed each one |
| `lib/profiles.py` | per leaf: the axis its listings vary along, its spec keys, price band, tags |
| `lib/geo.py` | a point per province, so listings have a location |
| `lib/paths.py` | where everything lives, so no module guesses from its own `__file__` |
| `lib/crawl.py` | harvests real Vietnamese product names into `vocab.json` |
| `lib/photos.py` | downloads each crawled product's own photograph |
| `lib/generate.py` | writes `out/*.tsv` and `out/load.sql` |
| `sql/categories.sql` | merges the duplicate categories, builds the two-level tree |
| `sql/embed_queue.sql` | marks a bounded subset for the indexer |
| `sql/wipe.sql` | removes everything `generate.py` wrote, and nothing else |
| `proxies.txt` | gitignored; see the throttling section |

Generated and gitignored: `vocab.json`, `photos/`, `photos_manifest.json`, `out/`.

## Running it on a machine that has never seen it

```sh
docker exec -w /src server-gateway-dev-1 go run ./cmd/migrate   # needs migration 012
./seed all --total 1000000 --embed-after
```

Budget about four hours before the embedding starts: most of it is the crawl, which is paced to
stay under Tiki's per-address limit, and half an hour is twenty thousand photographs.

That is the whole thing: it builds the category tree, crawls names if `vocab.json` is missing,
downloads and installs the photographs, loads a million listings in three passes, and verifies
the result. Run it twice and the second run loads nothing — every step checks before doing.

Step by step, if something needs redoing on its own:

```sh
./seed categories                     # idempotent
./seed crawl                          # 3000/leaf; --resume picks up leaves still short
./seed photos --install               # ~20k images, ~4GB
./seed load --total 1000000 --steps 3
./seed embed --per-leaf 2941          # ~100k, spread evenly over the leaves
./seed wipe --yes                     # start over
```

`--total` is the size the catalogue should **end up**, not how many rows to add. Each leaf is
topped up to `total / leaves`; a leaf already over that is left alone rather than having real
listings deleted. `--ignore-existing` on `lib/generate.py` adds an equal number to every leaf
instead, keeping whatever skew is there.

The load is split into steps because one transaction carrying a million listings and their
variants, reviews, tags and signals is several gigabytes of WAL against a `max_wal_size` of one
— and because a failure then costs the whole run instead of one step.

## Tiki throttles, and the pool needs a slow pace rather than a big one

The API is fronted by a bot check that decides on the TLS handshake — Python's `urllib` gets
an 18KB challenge page whatever headers it sends, which is why `fetch()` shells out to `curl`.

Past that it rate-limits per address, and getting this wrong cost most of an afternoon, so the
measurements are worth writing down:

- A single address managed roughly **ten requests inside ten minutes** before every
  subsequent response came back as the challenge page.
- The block is a **cooldown of about an hour**, not a ban. Thirty addresses that were all
  being challenged answered normally again later the same afternoon, unchanged.
- Concurrency is not the lever. Six workers over twenty proxies at one request per address
  every eight seconds is only 2.5 req/s in total, and it still lost the whole pool, because
  the limit is per address and each of the twenty was doing a request every eight seconds.

Two mistakes followed from misreading that. The first was pacing the pool as a whole instead
of pacing each address — `Proxies.take()` now holds a per-address clock, which is what
`--per-proxy` sets. The second was benching a challenged address for ninety seconds: it came
straight back while still inside its cooldown, was challenged again, and the pool emptied in
minutes. `--bench` now defaults to forty-five minutes, which is long enough to sit it out.

The defaults follow the measurement with an order of magnitude to spare: one request per
address every ninety seconds. Thirty proxies is then about 20 req/min, and the thousand-odd
requests a 600-name crawl needs take roughly an hour. Slower than it looks like it should be,
and it finishes, which the fast settings never did.

`--proxies FILE` takes one URL per line, `#` comments ignored. Two things to check when a
whole batch fails at once:

- **`curl: (56) Proxy CONNECT aborted` on every one** means they are SOCKS5, not HTTP. Use
  `socks5h://` — with plain `socks5://` curl resolves DNS locally and it fails anyway.
- **A challenge page through the proxy, but `https://ifconfig.me` through it works**, means
  the proxy is fine and Tiki has benched that address. Wait; it comes back.

Residential addresses pass the bot check. Datacenter ranges are widely pre-blocked, so a large
datacenter pool can fail entirely where a small residential one works.

The generator does not need the crawl to finish: it merges `vocab.json` with the names already
in the database and with `profiles.FALLBACK`, so it runs with no network at all — just with
more repetition, and it folds a second attribute into the name for any leaf whose pool cannot
cover its target.

## What gets written

| Table | What lands there |
|---|---|
| `account.account` | the sellers, and a larger pool of buyers who write the reviews |
| `account.contact` | one default pickup contact per seller |
| `catalog.listing` | the listing, with a location point |
| `catalog.variant`, `catalog.stock` | one to four variants each, with stock |
| `catalog.tag`, `catalog.listing_tag` | the brand the crawl found, plus a few of the leaf's own |
| `trust.review` | the reviews `cached_rating` is the average of |
| `catalog.favorite`, `catalog.listing_signal` | sparse, so the recommended feed has an input |
| `observability.listing_popularity` | so the trending list has more than a handful of rows |

Not written: orders, photographs, embeddings.

**The rating comes out of the reviews, not alongside them.** The first version wrote
`cached_rating` and `cached_review_count` straight onto the listing and left `trust.review`
empty, which put "43 đánh giá" on a product page whose review list had nothing in it. The
reviews are generated first now and the two cached columns are computed from them; `load.sql`
asserts it afterwards and prints `cached_review_count matches trust.review on every listing`,
or warns with a count.

One thing is still inconsistent, deliberately: `trust.review.order_id` is NOT NULL and names no
order row. The column has no foreign key — the order module is another schema and cannot be
joined — and the alternative was generating several hundred thousand orders through the order
and finance schemas, which is a bigger job than this whole command.

The contact is not decoration: `listing.province_code` and `ward_code` are copied from the
seller's default pickup contact exactly as `PublishListing` does it, and the browse feed's area
filter reads them. A seller without one owns listings no area filter can see. The location
point matters for the same reason one level down — `ST_DWithin` treats a NULL point as outside
every radius, so a listing without one is a listing "near me" never returns. `geo.py` holds a
point per province and the generator jitters around it, because `areas/vn.json` carries codes
and names but no coordinates.

Galleries reuse the resources `cmd/seed` already generated, so cards render. The same photo
appears on many listings — a hundred thousand rendered photographs is gigabytes and hours, and
a repeated picture reads better than none.

The tag vocabulary is kept small on purpose. Every distinct tag gets a row in `tag` and a
vector in `tag_embedding`, which measures 15KB per row here, so a vocabulary that grew with the
catalogue would cost more than the listings do.

Ids are assigned by `generate.py`, not by the identity columns, because `variant.listing_id`,
`stock.variant_id` and `review.listing_id` have to point at rows that do not exist yet.
`load.sql` re-checks the high-water mark of every table it writes before inserting and moves
the sequences afterwards, so it fails loudly rather than colliding if something else wrote in
between.

## The category tree needs migration 012

`sql/categories.sql` builds eight roots over thirty-four leaves, and every listing sits on a
leaf. The browse feed and the search predicate filtered categories with `l.category_id = $1`,
which was right when the tree was flat and matches nothing at all against a root — the top
level of the menu returned an empty page. Migration `012_category_subtree.sql` adds
`category_subtree(bigint)`, and both call sites now ask
`l.category_id IN (SELECT category_subtree($1))`. The function is STABLE and PARALLEL SAFE, so
the planner runs it once as an InitPlan and hashes the result rather than calling it per row.

Apply it before loading.

## What is still invented, measured

Counted on a million generated listings, because "it looks fine" is not a measurement:

| Field | Distinct | Repeats | Verdict |
|---|---|---|---|
| `listing.description` | 705 251 | 1.4× | fine — the product name is interpolated into it |
| `review.body` | 8 300 per 8 621 | 1.04× | fine, for the same reason |
| `listing.name` | depends on the crawl | see below | the one number that is worth crawling longer for |
| `listing.specifications` | 78 956 | 13× | honest: "Chất liệu: Cotton" genuinely repeats |

**Names are bounded by the crawl, not by the generator.** A listing's name is a crawled product
name plus the axis values that distinguish it, so the distinct-name ceiling is
`base names × axis combinations` — around fifteen combinations per name here. At `--per-leaf 600`
that is ~210 000 names, and a million listings drawn from it repeated the worst name **fifty-two
times identically**, which is what a category page shows a reader. The fix is more real names,
not more permutations: `--per-leaf 3000` is the default for that reason. Permuting harder is
just fabricating harder.

Some leaves cannot be helped by depth. Tiki lists about 344 phones in total, so
`Điện thoại & Máy tính bảng` stops at ~100 base names however deep the crawl goes, and its
listings lean on the axis values instead.

The reviews were not always fine. The first version drew one of **eighteen** fixed sentences,
which at this size meant each appeared 136 620 times. They are composed now — an opening, a
middle naming this product's own name, variant or specification, and a closing — which is the
same trick that always kept the descriptions varied.

Covers are each product's real photograph when `./seed photos --install` has run. Before that they
were drawn from every resource in the catalogue at random, which put a bicycle on a rice cooker;
the fallback is now a per-leaf pool.

One thing is still inconsistent by choice: `trust.review.order_id` names no order row. See the
section above.

## One listing per name, and what that costs

The requirement that settled: **no two listings share a name.** Three attempts narrowed it
without meeting it — crawling four times as many real names took the average repeat from 4.8× to
2.45×, and jittering prices to 100đ stopped same-name-same-price pairs sitting adjacent in a
search result — but narrowing is not removing. `sql/dedup_names.sql` keeps one row per name and
deletes the rest.

The price is the even distribution, and it is not a small one:

| | before | after |
|---|---|---|
| listings | 1 000 000 | **407 835** |
| sharing a name | 592 165 | **0** |
| rows per leaf | 29 411 .. 29 412 | **1 889 .. 17 492** |

`Nhạc cụ` keeps 1 889 rows because Tiki lists about six hundred distinct musical instruments and
no crawl depth changes that; `Thời trang & Quần áo` keeps 17 492. A catalogue shaped by what
exists is lopsided, which is also what the real thing looks like. `./seed verify` reports the
spread now and asserts uniqueness instead — the invariant changed, so the check had to.

Which row survives a group: the one that already carries an embedding, then the lowest id.
Embedding is the expensive thing here, and preferring the embedded row kept 46 127 vectors where
picking by id would have kept 32 000.

**Before deleting at this scale, check that every cascading foreign key has an index.**
`listing_signal_listing_id_fkey` is ON DELETE CASCADE and the table's two indexes both led with
`account_id`, so Postgres's per-row referential trigger did a sequential scan of 2.3M rows for
*every listing deleted*: 900 seconds without finishing a single 50 000-row batch, and the
storefront's search went to 40 seconds while it ran. Migration `013_listing_signal_listing_id.sql`
adds the index; the same work then deleted 500 000 rows in 60 seconds. The soft delete on
`listing` is why nobody had noticed — an ordinary takedown never fires the cascade.

## The integration tests and this data do not mix

`go test -tags integration ./...` shares one schema with whatever is loaded, and those tests
assume the schema holds only their own fixtures. With a million listings in it three of them
fail: the stale-interest queries have a LIMIT that ten thousand generated buyers with 918 000
favourites crowd the fixture out of, and the fused search legs return real listings ahead of it.
Run them **before** loading, or point `CATALOG_DB_DSN` at a database this tool has never touched.
Do not chase the failures one at a time — they are all the same assumption.

One of them was worth fixing rather than working around: the favourites case hard-coded
`buyer = 4242` and asserted that account had exactly one favourite, which was true only while the
schema held nothing but fixtures. Account 4242 is `bulk_buyer_004242`, with 136 of its own.

## Search

Nothing here is embedded. Retrieval is bge-m3 dense+sparse only — migration `008_search.sql`
dropped the trigram index — so **a listing loaded by this command is a listing search does not
find** until the indexer has been over it. Browse, category pages, price and area filters all
work immediately.

To queue them, generate with `--embed-queue`, then run the embedder. Measured against the
bge-m3 service in `config.dev.yml`, warm, on text the length these listings actually produce:
**~9 docs/s**, so 100 000 listings is about **3 hours**. The first batch is much slower — that
is the model loading, not the throughput.

## How far this goes

Measured, not estimated — the estimates were wrong by more than a factor of two in both
directions, so these are what a real million-row load actually cost:

| | measured |
|---|---|
| 1 000 000 listings + variants, stock, reviews, tags, favourites, signals, popularity | **3.6 GB** |
| the same, per 100k | ~380 MB |
| `listing_embedding` per row (table + dense HNSW + sparse HNSW) | **18.4 KB** |
| 100 000 embedded | ~1.8 GB |
| 1 000 000 embedded | ~18 GB |

The per-row figures published earlier were taken from a table with 858 rows in it, where index
overhead per row is much higher: they predicted 8 GB for the catalogue and it came to 3.6.

**Disk is not the constraint; RAM for the vector indexes is.** Fully embedding a million
listings puts ~18 GB of vectors against 19 GiB of physical memory, and swap does not help —
HNSW traversal is random access, so a graph paged out is a graph read from disk on every hop.
Roughly 70% of it stays cached on this machine and the rest comes off NVMe per query, which is
slower but not the thrashing an earlier note in this file claimed.

Index *build* is not the constraint either, which is the non-obvious part: embedding arrives at
about 16 documents a second because the model is the bottleneck, and HNSW takes inserts orders
of magnitude faster than that. So the sensible shape at a million rows is all of it loaded and a
bounded subset embedded — `./seed embed --per-leaf 2941` is ~100 000 spread evenly over the
leaves, whose dense index is under a gigabyte and sits in cache comfortably.

Timings on this hardware, for planning:

| | |
|---|---|
| crawl, `--per-leaf 3000` | ~1 h (paced under Tiki's per-address limit) |
| photographs, ~60k images | ~25 min, ~6 GB |
| load 1M, three passes | ~25 min |
| embed 100k | ~1.8 h at 15.8 docs/s |
| embed 1M | ~17 h |
