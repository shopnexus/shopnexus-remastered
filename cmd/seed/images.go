package main

import (
	"bytes"
	"embed"
	"encoding/json/v2"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
)

// Product photos, where they come from, and where they do not.
//
// The dump this seeder used to read hotlinked a marketplace's own CDN: every listing pointed at
// somebody else's photograph of somebody else's product, served from their servers. That was a
// copyright problem and a dependency the other side could switch off at any moment — and did,
// which is why the seeded galleries render empty today.
//
// What replaced it is in photos/: real photographs, downloaded once and committed, every one of
// them CC0 or public domain from Wikimedia Commons (which includes the Unsplash archive donated
// under CC0). Nothing is fetched at run time, so the seeder needs no network and no link can
// rot. photos/manifest.json records, for each file, the page it came from, who took it and under
// what licence; photos/ATTRIBUTION.md is the same thing for a human, and it is what a report
// cites. Nothing here came from a marketplace.
//
// Where the free-licence pools have no photograph of the thing — a Vietnamese áo dài, a
// countertop air fryer, a portable SSD — the listing keeps a drawn placeholder instead: a studio
// sweep, a floor shadow and a rounded plate where the product would stand, in a palette hashed
// from the slug. A deliberate stand-in reads better than a photograph of the wrong object, and
// it is also the fallback if a photo file is ever missing, so a lost file degrades the gallery
// instead of breaking the run.
type photo struct {
	// key is the object key, and with the provider it is the resource row's identity. Its
	// extension follows mime, because the two disagreeing is how a gallery ends up serving a
	// PNG that a browser was told is a JPEG.
	key  string
	mime string
	// source is the path inside photosFS. Empty means there is no photograph for this slot and
	// the bytes are drawn.
	source string
	// seedText is what the drawn palette and pattern are derived from — the listing's slug, so
	// every drawn photo of one listing is a variation on one colour scheme and a gallery looks
	// like a gallery rather than a bag of unrelated tiles.
	seedText string
	index    int
	// size is filled in once the bytes are written; the resource row records it.
	size int64
}

// credit is one row of photos/manifest.json. It is the provenance of a picture that ends up in
// a submitted report, so it is carried in the repository rather than reconstructed later.
type credit struct {
	File       string `json:"file"`
	Subject    string `json:"subject"`
	Title      string `json:"title"`
	Author     string `json:"author"`
	License    string `json:"license"`
	LicenseURL string `json:"license_url"`
	Source     string `json:"source"`
	SourceURL  string `json:"source_url"`
}

// photosFS is the committed photo library. Embedded rather than read from disk for the same
// reason the dataset is: cmd/seed runs as a container with no bind mount of the source tree.
//
//go:embed photos/manifest.json photos/*.jpg
var photosFS embed.FS

// photoLibrary is the manifest grouped the way the planner asks for it: which files exist for a
// subject, in a stable order.
type photoLibrary struct {
	bySubject map[string][]credit
	credits   []credit
}

func loadPhotoLibrary() (*photoLibrary, error) {
	raw, err := photosFS.ReadFile("photos/manifest.json")
	if err != nil {
		return nil, fmt.Errorf("read photo manifest: %w", err)
	}
	var credits []credit
	if err := json.Unmarshal(raw, &credits); err != nil {
		return nil, fmt.Errorf("parse photo manifest: %w", err)
	}
	lib := &photoLibrary{bySubject: map[string][]credit{}, credits: credits}
	for _, c := range credits {
		// A manifest row whose file is not actually in the tree would otherwise become a
		// zero-byte resource and an empty gallery slot. Drop it and let the drawing take over.
		if _, err := fs.Stat(photosFS, "photos/"+c.File); err != nil {
			continue
		}
		lib.bySubject[c.Subject] = append(lib.bySubject[c.Subject], c)
	}
	for _, list := range lib.bySubject {
		sort.Slice(list, func(i, j int) bool { return list[i].File < list[j].File })
	}
	return lib, nil
}

// forSubject returns the photograph for slot n of a listing on this subject, or false when the
// library has run out and the slot should be drawn.
func (l *photoLibrary) forSubject(subject string, n int) (credit, bool) {
	list := l.bySubject[subject]
	if n >= len(list) {
		return credit{}, false
	}
	return list[n], true
}

const (
	photoWidth  = 720
	photoHeight = 720
	// The committed photographs are JPEG, centre-cropped square at 640px; anything drawn here
	// is PNG. Both are on storage.allowed_mimes.
	jpegMime  = "image/jpeg"
	drawnMime = "image/png"
	// quantum flattens each channel onto a small ladder of values. Invisible at a glance and
	// worth roughly three quarters of the file size: a PNG of a perfectly smooth gradient is
	// nearly incompressible, and a hundred and fifty of them at full depth is thirty megabytes
	// of demo data nobody wants in a volume.
	quantum = 3
)

// palettes are picked by hashing the slug. Muted and slightly desaturated on purpose: a
// saturated primary next to a product card's own colours reads as an error state.
var palettes = [][2]color.RGBA{
	{{0x1f, 0x36, 0x4d, 0xff}, {0x3f, 0x7c, 0xac, 0xff}},
	{{0x2d, 0x3a, 0x2e, 0xff}, {0x6b, 0x9a, 0x6f, 0xff}},
	{{0x4a, 0x2c, 0x2a, 0xff}, {0xa8, 0x6b, 0x54, 0xff}},
	{{0x2b, 0x2b, 0x3c, 0xff}, {0x6f, 0x63, 0x9b, 0xff}},
	{{0x3d, 0x33, 0x1f, 0xff}, {0xb0, 0x8d, 0x4f, 0xff}},
	{{0x1e, 0x3c, 0x3a, 0xff}, {0x4f, 0x9a, 0x94, 0xff}},
	{{0x40, 0x28, 0x38, 0xff}, {0x9b, 0x63, 0x84, 0xff}},
	{{0x30, 0x34, 0x38, 0xff}, {0x7e, 0x8a, 0x93, 0xff}},
}

// writePhotos puts every planned photo into the object store — copied from the committed
// library where there is one, drawn where there is not — and records how big each came out.
// Returns the bytes written and how many of them were real photographs.
//
// root is the storage root the gateway serves `local` objects from. A seeder running outside
// the gateway's filesystem cannot reach it, which is why cmd/seed now ships in the image and
// runs as a compose service with the same volume mounted — see docker-compose.yml.
func writePhotos(root string, photos []photo) (bytes int64, real int, err error) {
	for i := range photos {
		p := &photos[i]
		full := filepath.Join(root, filepath.FromSlash(p.key))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return 0, 0, fmt.Errorf("create object directory for %s: %w", p.key, err)
		}
		var body []byte
		if p.source != "" {
			body, err = photosFS.ReadFile(p.source)
			if err != nil {
				return 0, 0, fmt.Errorf("read committed photo %s: %w", p.source, err)
			}
			real++
		} else {
			body, err = encodeDrawn(p.seedText, p.index)
			if err != nil {
				return 0, 0, fmt.Errorf("draw object %s: %w", p.key, err)
			}
		}
		// 0644 rather than the default: the seeder runs as root inside the compose service
		// (the named volume starts root-owned) and the gateway that serves these runs as
		// nonroot, so an unreadable file is an empty gallery.
		if err := os.WriteFile(full, body, 0o644); err != nil {
			return 0, 0, fmt.Errorf("write object %s: %w", p.key, err)
		}
		p.size = int64(len(body))
		bytes += p.size
	}
	return bytes, real, nil
}

func encodeDrawn(seedText string, index int) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, drawPhoto(seedText, index)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// drawPhoto composes a studio-style placeholder: a swept background, a soft floor shadow, and a
// rounded plate standing where the product would be. The plate is what stops it reading as a
// broken image — a bare gradient in a gallery slot looks like a load that failed, and a reader
// of the report should be able to tell at a glance that the picture is a stand-in rather than a
// photograph that did not arrive.
func drawPhoto(seedText string, index int) image.Image {
	h := fnv.New64a()
	_, _ = h.Write([]byte(seedText))
	sum := h.Sum64()

	pal := palettes[sum%uint64(len(palettes))]
	dark, light := pal[0], pal[1]
	// Each photo of one listing is the same palette lit from a slightly different angle, so a
	// gallery reads as several shots of one thing rather than as several unrelated things.
	angle := float64(index)*0.6 + float64(sum%17)/17*math.Pi

	img := image.NewRGBA(image.Rect(0, 0, photoWidth, photoHeight))
	cx, cy := float64(photoWidth)/2, float64(photoHeight)/2
	maxDist := math.Hypot(cx, cy)
	ax, ay := math.Cos(angle), math.Sin(angle)

	// The plate: its proportions shift per shot, the way a second photograph of one object is
	// taken from further away or turned on its side.
	hw := 0.30 * float64(photoWidth) * (1 + 0.10*math.Sin(float64(index)*1.9+float64(sum%7)))
	hh := 0.30 * float64(photoHeight) * (1 + 0.10*math.Cos(float64(index)*2.3+float64(sum%5)))
	radius := math.Min(hw, hh) * 0.22
	plateY := cy - 0.02*float64(photoHeight)

	for y := range photoHeight {
		for x := range photoWidth {
			fx, fy := float64(x)-cx, float64(y)-cy
			// Linear sweep along the lit axis, then a vignette so the middle is where the eye
			// goes — the same two things a product photograph gets in post.
			t := clamp01((fx*ax+fy*ay)/(maxDist*1.6) + 0.5)
			v := 1 - 0.30*math.Pow(math.Hypot(fx, fy)/maxDist, 2)
			r := mix(dark.R, light.R, t) * v
			g := mix(dark.G, light.G, t) * v
			b := mix(dark.B, light.B, t) * v

			// Floor shadow: an ellipse under the plate, darkening what is behind it.
			sx := (float64(x) - cx) / (hw * 1.15)
			sy := (float64(y) - (plateY + hh*0.92)) / (hh * 0.20)
			if d := sx*sx + sy*sy; d < 1 {
				shade := 1 - 0.45*(1-d)
				r, g, b = r*shade, g*shade, b*shade
			}

			// The plate itself, with a top-lit face so it has volume rather than being a
			// sticker. Anti-aliased over a pixel and a half, or the corners look like stairs.
			if cover := plateCoverage(float64(x)-cx, float64(y)-plateY, hw, hh, radius); cover > 0 {
				lift := 0.55 + 0.35*clamp01(1-(float64(y)-(plateY-hh))/(2*hh))
				pr := mix(dark.R, light.R, clamp01(t*0.4+lift))
				pg := mix(dark.G, light.G, clamp01(t*0.4+lift))
				pb := mix(dark.B, light.B, clamp01(t*0.4+lift))
				r = r*(1-cover) + pr*cover
				g = g*(1-cover) + pg*cover
				b = b*(1-cover) + pb*cover
			}

			img.SetRGBA(x, y, color.RGBA{R: quantize(r), G: quantize(g), B: quantize(b), A: 0xff})
		}
	}
	return img
}

// plateCoverage is how much of this pixel the rounded plate covers, 0 outside and 1 well
// inside. The distance is the standard rounded-rectangle one: shrink the box by the corner
// radius, measure to that, then subtract the radius back off.
func plateCoverage(dx, dy, hw, hh, radius float64) float64 {
	ex := math.Max(math.Abs(dx)-(hw-radius), 0)
	ey := math.Max(math.Abs(dy)-(hh-radius), 0)
	dist := math.Hypot(ex, ey) - radius
	const edge = 1.5
	switch {
	case dist <= -edge:
		return 1
	case dist >= 0:
		return 0
	default:
		return -dist / edge
	}
}

func mix(from, to uint8, t float64) float64 {
	return float64(from) + (float64(to)-float64(from))*t
}

func quantize(v float64) uint8 {
	n := int(clamp01(v/255)*255) / quantum * quantum
	return uint8(min(n, 255))
}

func clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}
