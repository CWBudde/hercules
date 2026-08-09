package modes

import (
	"image"
	"image/draw"
	_ "image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestD5bBarNotMattedOverWhite guards PLAN.md D5b: a 0.8-alpha bar rendered
// through the report-figure pipeline must keep its pure straight RGB
// (#E91E63 -> ~233,30,99,204), not the white-matted (237,75,130,204) produced
// by the old agg_go fill blender. Regression guard for the agg_go pin: labours
// must build against an agg_go whose fill blend weights the destination RGB by
// destination alpha (local ../agg_go, ahead of the published v0.2.31).
func TestD5bBarNotMattedOverWhite(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "ownership-concentration.png")
	err := plotOwnershipSubsystemsBar("repro", "",
		map[string]float64{"alpha": 0.9}, map[string]float64{"alpha": 0.8}, out)
	if err != nil {
		t.Fatalf("plot: %v", err)
	}
	pngs, _ := filepath.Glob(filepath.Join(dir, "*.png"))
	if len(pngs) == 0 {
		t.Fatalf("no PNG written")
	}
	f, err := os.Open(pngs[0])
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	nr := image.NewNRGBA(img.Bounds())
	draw.Draw(nr, nr.Bounds(), img, img.Bounds().Min, draw.Src)
	b := nr.Bounds()
	near := func(r, g, bb uint8, w [3]int) bool {
		d := func(a, b int) int {
			if a > b {
				return a - b
			}
			return b - a
		}
		return d(int(r), w[0]) <= 8 && d(int(g), w[1]) <= 8 && d(int(bb), w[2]) <= 8
	}
	var pure, matted int
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := nr.NRGBAAt(x, y)
			if c.A == 0 {
				continue
			}
			if near(c.R, c.G, c.B, [3]int{233, 30, 99}) {
				pure++
			}
			if near(c.R, c.G, c.B, [3]int{237, 75, 130}) {
				matted++
			}
		}
	}
	if pure == 0 || matted > pure {
		t.Fatalf("Gini bar matted over white (%d matted vs %d pure) — agg_go fill-blend regression (D5b)", matted, pure)
	}
}
