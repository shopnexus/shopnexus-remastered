package main

import (
	_ "embed"
	"encoding/json/v2"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// product is the subset of assets/data.json this seeder reads. The file is a scrape, so
// several fields arrive as either a number or a string ("stock") and several are empty
// throughout ("sold", "reviews"); what is derived rather than read is noted at the use site.
type product struct {
	Title          string      `json:"title"`
	Rating         float64     `json:"rating"`
	InitialPrice   float64     `json:"initial_price"`
	FinalPrice     float64     `json:"final_price"`
	Currency       string      `json:"currency"`
	Stock          any         `json:"stock"`
	Image          []string    `json:"image"`
	Breadcrumb     []string    `json:"breadcrumb"`
	Specifications []spec      `json:"Product Specifications"`
	Description    string      `json:"Product Description"`
	Variations     []variation `json:"variations"`
	Brand          string      `json:"brand"`
}

type spec struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type variation struct {
	Name       string   `json:"name"`
	Variations []string `json:"variations"`
}

func loadProducts(path string) ([]product, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read product data: %w", err)
	}
	var out []product
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse product data: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no products in %s", path)
	}
	return out, nil
}

// The category tree, embedded rather than read from disk: it is this command's own data,
// unlike the product dump, which is far too large to compile in.
//
//go:embed categories.json
var categoriesJSON []byte

type category struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// fallbackCategory catches every breadcrumb the index cannot place. A listing must have a
// category — "listing"."category_id" is NOT NULL — so there has to be one that always matches.
var fallbackCategory = category{Name: "General", Description: "Uncategorized products"}

func loadCategories() ([]category, error) {
	var out []category
	if err := json.Unmarshal(categoriesJSON, &out); err != nil {
		return nil, fmt.Errorf("parse categories: %w", err)
	}
	return append(out, fallbackCategory), nil
}

// categoryIndex maps a product's breadcrumb onto one of our categories. The breadcrumbs are
// the source marketplace's own taxonomy in eleven locales, so this is a keyword guess and
// not a translation. It exists because the English category names alone place fewer than
// one listing in eight, and a tree where everything is "General" is not a tree.
type categoryIndex struct {
	byName map[string]string // whole lowercased name -> category name
	byWord map[string]string // single word -> category name; last writer wins
}

// \p{M} is in the class, not out of it: a Thai vowel sign is a combining mark, so without
// it a Thai crumb is chopped into fragments that match nothing and the whole locale falls
// through to "General".
var wordSplit = regexp.MustCompile(`[^\p{L}\p{N}\p{M}]+`)

func newCategoryIndex(cats []category) *categoryIndex {
	idx := &categoryIndex{byName: map[string]string{}, byWord: map[string]string{}}
	for _, c := range cats {
		lower := strings.ToLower(c.Name)
		idx.byName[lower] = c.Name
		for _, w := range wordSplit.Split(lower, -1) {
			if len(w) > 2 { // "of", "&" and friends would match everything
				idx.byWord[w] = c.Name
			}
		}
	}
	// After the English names, so a deliberate alias wins where a category name is
	// ambiguous: "fashion" appears in two of them and would otherwise land on whichever
	// was declared last.
	for word, name := range categoryAliases {
		idx.byWord[word] = name
	}
	return idx
}

// match walks the breadcrumb leaf-first, because the deepest crumb is the most specific
// thing the source knew about the product. Each word is tried raw and then folded, so an
// accented Spanish crumb hits an alias written in ASCII while a Thai one — which folds to
// nothing — still matches on the raw token.
func (idx *categoryIndex) match(breadcrumb []string) string {
	for i := len(breadcrumb) - 1; i >= 0; i-- {
		if name, ok := idx.byName[strings.ToLower(breadcrumb[i])]; ok {
			return name
		}
	}
	for i := len(breadcrumb) - 1; i >= 0; i-- {
		for _, w := range wordSplit.Split(strings.ToLower(breadcrumb[i]), -1) {
			if name, ok := idx.byWord[w]; ok {
				return name
			}
			if name, ok := idx.byWord[slugify(w)]; ok {
				return name
			}
		}
	}
	return fallbackCategory.Name
}

// categoryAliases is the locale vocabulary of the source taxonomy: Spanish, Portuguese,
// Indonesian/Malay, Vietnamese (written folded, since that is what a crumb reduces to),
// Thai and Chinese (written whole, since neither splits into words). Best effort by
// construction — this is seed data, and the alternative is one bucket.
var categoryAliases = map[string]string{
	// Spanish / Portuguese
	"deportes": "Sports & Outdoors", "deporte": "Sports & Outdoors", "esportes": "Sports & Outdoors",
	"aire": "Sports & Outdoors", "ciclismo": "Sports & Outdoors",
	"ropa": "Fashion & Clothing", "moda": "Fashion & Clothing", "roupas": "Fashion & Clothing",
	"camisas": "Fashion & Clothing", "vestidos": "Fashion & Clothing", "pantalones": "Fashion & Clothing",
	"hogar": "Home Decor & Lighting", "casa": "Home Decor & Lighting", "decoracion": "Home Decor & Lighting",
	"muebles": "Home Furniture", "cocina": "Kitchen & Dining",
	"hobbies": "Toys & Games", "colecciones": "Toys & Games", "juguetes": "Toys & Games",
	"belleza": "Beauty & Personal Care", "cuidado": "Beauty & Personal Care", "beleza": "Beauty & Personal Care",
	"salud": "Health & Wellness", "saude": "Health & Wellness",
	"motocicletas": "Automotive & Motorcycle", "motos": "Automotive & Motorcycle",
	"automotriz": "Automotive & Motorcycle", "vehiculos": "Automotive & Motorcycle",
	"accesorios": "Fashion Accessories",
	"madre":      "Baby & Maternity", "bebes": "Baby & Maternity", "bebe": "Baby & Maternity",
	"ninos": "Baby & Maternity", "infantil": "Baby & Maternity",
	"viajes": "Bags & Luggage", "equipaje": "Bags & Luggage", "bolsas": "Bags & Luggage",
	"bolsos": "Bags & Luggage", "mochilas": "Bags & Luggage",
	"zapatos": "Shoes & Footwear", "calzado": "Shoes & Footwear", "sapatos": "Shoes & Footwear",
	"relojes": "Watches", "joyeria": "Jewelry",
	"celulares": "Mobile Phones & Tablets", "telefonia": "Mobile Phones & Tablets",
	"computacion": "Computers & Networking", "computadoras": "Computers & Networking",
	"electronica": "Electronics", "audio": "Electronics", "electrodomesticos": "Small Appliances",
	"alimentos": "Grocery & Food", "comida": "Grocery & Food", "bebidas": "Grocery & Food",
	"mascotas": "Pet Supplies", "herramientas": "Tools & Hardware", "libros": "Books & Media",
	"papeleria": "Stationery & Office Supplies", "jardin": "Garden & Outdoor Living",
	"musica": "Musical Instruments", "videojuegos": "Video Games & Gaming",
	"camaras": "Electronics", "drones": "Electronics", "eletrodomesticos": "Small Appliances",
	"clothes": "Fashion & Clothing",

	// Indonesian / Malay
	"olahraga": "Sports & Outdoors", "sukan": "Sports & Outdoors",
	"pakaian": "Fashion & Clothing", "fashion": "Fashion & Clothing", "busana": "Fashion & Clothing",
	"rumah": "Home Decor & Lighting", "perlengkapan": "Home Decor & Lighting", "dapur": "Kitchen & Dining",
	"perabot":    "Home Furniture",
	"kecantikan": "Beauty & Personal Care", "perawatan": "Beauty & Personal Care",
	"kesehatan": "Health & Wellness", "kesihatan": "Health & Wellness",
	"bayi": "Baby & Maternity", "anak": "Baby & Maternity", "ibu": "Baby & Maternity",
	"makanan": "Grocery & Food", "minuman": "Grocery & Food",
	"sepatu": "Shoes & Footwear", "kasut": "Shoes & Footwear",
	"otomotif": "Automotive & Motorcycle", "motor": "Automotive & Motorcycle",
	"handphone": "Mobile Phones & Tablets", "ponsel": "Mobile Phones & Tablets",
	"komputer": "Computers & Networking", "laptop": "Computers & Networking",
	"elektronik": "Electronics", "mainan": "Toys & Games", "buku": "Books & Media",
	"perhiasan": "Jewelry", "hewan": "Pet Supplies", "taman": "Garden & Outdoor Living",
	"koleksi": "Toys & Games", "hobi": "Toys & Games",

	// Vietnamese, folded
	"thao": "Sports & Outdoors", "trang": "Fashion & Clothing",
	"dung": "Small Appliances", "thanh": "Electronics",
	"dep": "Beauty & Personal Care", "khoe": "Health & Wellness",
	"pham": "Grocery & Food", "uong": "Grocery & Food",
	"giay": "Shoes & Footwear", "tui": "Bags & Luggage",
	"choi": "Toys & Games", "sach": "Books & Media",
	"nha": "Home Decor & Lighting", "thoai": "Mobile Phones & Tablets",
	"tas": "Bags & Luggage",

	// Thai and Chinese, whole tokens: neither script splits into words
	"เสื้อผ้าผู้ชาย":                "Fashion & Clothing",
	"เสื้อผ้าผู้หญิง":               "Fashion & Clothing",
	"ผลิตภัณฑ์บำรุงและเสริมความงาม": "Beauty & Personal Care",
	"กลุ่มผลิตภัณฑ์เพื่อสุขภาพ":     "Health & Wellness",
	"กีฬาและกิจกรรมกลางแจ้ง":        "Sports & Outdoors",
	"บ้านและไลฟ์สไตล์":              "Home Decor & Lighting",
	"อาหารและเครื่องดื่ม":           "Grocery & Food",
	"แม่และเด็ก":                    "Baby & Maternity",
	"รองเท้าผู้หญิง":                "Shoes & Footwear",
	"รองเท้าผู้ชาย":                 "Shoes & Footwear",
	"กระเป๋าเดินทางและกระเป๋าถือ":   "Bags & Luggage",
	"มือถือและอุปกรณ์เสริม":         "Mobile Phones & Tablets",
	"เครื่องใช้ไฟฟ้าภายในบ้าน":      "Large Appliances",
	"เครื่องใช้ในบ้าน":              "Home Decor & Lighting",
	"คอมพิวเตอร์และอุปกรณ์เสริม":    "Computers & Networking",
	"กระเป๋าผู้หญิง":                "Bags & Luggage",
	"เครื่องเสียง":                  "Electronics",
	"เครื่องประดับ":                 "Jewelry",
	"เสื้อผ้าแฟชั่นเด็ก":            "Baby & Maternity",
	"รถยนต์": "Automotive & Motorcycle",
	"保健":     "Health & Wellness",
	"母嬰用品":   "Baby & Maternity",
	"美妝保養":   "Beauty & Personal Care",
	"運動戶外":   "Sports & Outdoors",
	"居家生活":   "Home Decor & Lighting",
}
