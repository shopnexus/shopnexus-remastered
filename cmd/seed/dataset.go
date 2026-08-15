package main

import (
	_ "embed"
	"encoding/json/v2"
	"fmt"
)

// The demo marketplace, embedded rather than read from disk: it is this command's own data,
// it is small, and embedding it is what lets cmd/seed ship in the image and run inside the
// compose network — which is the only place the module DSNs resolve.
//
//go:embed dataset.json
var datasetJSON []byte

// dataset is the whole hand-written catalogue: the category tree the demo needs and the
// listings that sit in it. Nothing here is bootstrap — see the note on categories in
// writeCategories.
type dataset struct {
	Categories []category    `json:"categories"`
	Listings   []datasetItem `json:"listings"`
}

type category struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// datasetItem is one listing as a person would have written it. Prices are in the currency's
// smallest unit, which for VND is the đồng — the schema stores a BIGINT and does not care, but
// a variant priced at 6_200_000 has to mean six million two hundred thousand and not sixty-two
// thousand, so the unit is stated once here.
type datasetItem struct {
	Seller string `json:"seller"` // key into seedAccounts
	// Category is matched against dataset.Categories by name. A listing naming a category the
	// file does not declare is a bug in the file, and loadDataset says so rather than quietly
	// dropping the row.
	Category string `json:"category"`
	// PhotoSubject picks the listing's pictures out of the committed library in photos/. It is
	// the kind of thing rather than the exact model — the free-licence pools have a photograph
	// of "a mirrorless camera", not of this seller's particular A6400 — and a subject the
	// library has nothing for falls back to a drawn placeholder.
	PhotoSubject string            `json:"photo_subject"`
	Name         string            `json:"name"`
	Condition    string            `json:"condition"`  // new | used | damaged
	PriceMode    string            `json:"price_mode"` // fixed | negotiable
	Featured     bool              `json:"featured"`   // gets the richer demo history
	Description  string            `json:"description"`
	Specs        map[string]string `json:"specifications"`
	Tags         []string          `json:"tags"`
	// Images is how many pictures this listing's gallery has. The first is the cover. Slots are
	// filled from the committed photo library and drawn where it runs out — see images.go.
	Images   int              `json:"images"`
	Variants []datasetVariant `json:"variants"`
}

type datasetVariant struct {
	Attributes map[string]string `json:"attributes"`
	Price      int64             `json:"price"`
	Quantity   int64             `json:"quantity"`
}

func loadDataset() (*dataset, error) {
	var d dataset
	if err := json.Unmarshal(datasetJSON, &d); err != nil {
		return nil, fmt.Errorf("parse dataset: %w", err)
	}
	if len(d.Categories) == 0 || len(d.Listings) == 0 {
		return nil, fmt.Errorf("dataset is empty")
	}
	known := make(map[string]bool, len(d.Categories))
	for _, c := range d.Categories {
		known[c.Name] = true
	}
	byKey := map[string]bool{}
	for _, a := range seedAccounts {
		byKey[a.Key] = true
	}
	for i, l := range d.Listings {
		if !known[l.Category] {
			return nil, fmt.Errorf("listing %d (%q): unknown category %q", i, l.Name, l.Category)
		}
		if !byKey[l.Seller] {
			return nil, fmt.Errorf("listing %d (%q): unknown seller %q", i, l.Name, l.Seller)
		}
		if l.PhotoSubject == "" {
			return nil, fmt.Errorf("listing %d (%q): no photo_subject", i, l.Name)
		}
		if len(l.Variants) == 0 {
			return nil, fmt.Errorf("listing %d (%q): no variants", i, l.Name)
		}
		for j, v := range l.Variants {
			if v.Price <= 0 || v.Quantity <= 0 {
				return nil, fmt.Errorf("listing %d (%q) variant %d: price %d, quantity %d",
					i, l.Name, j, v.Price, v.Quantity)
			}
			if len(v.Attributes) == 0 {
				return nil, fmt.Errorf("listing %d (%q) variant %d: no attributes", i, l.Name, j)
			}
		}
	}
	return &d, nil
}

// currency is the whole marketplace's, because every seller in the demo is in Vietnam and the
// order module freezes one currency per listing. It is not a per-listing field for the same
// reason the dataset has no exchange rates: a C2C site in one country has one.
const currency = "VND"
