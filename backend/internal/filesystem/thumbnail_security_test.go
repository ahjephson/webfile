package filesystem

import (
	"image"
	"image/color"
	"testing"

	"github.com/kovidgoyal/imaging"
)

// CVE-2023-36308: malformed palette indices must not panic in resize workers.
func TestThumbnailResizeHandlesInvalidPaletteIndex(t *testing.T) {
	malformed := &image.Paletted{
		Pix: []byte{70}, Stride: 1, Rect: image.Rect(0, 0, 1, 1),
		Palette: color.Palette{color.Black},
	}
	thumbnail := imaging.Fit(malformed, 1, 1, imaging.Lanczos)
	if thumbnail.Bounds() != image.Rect(0, 0, 1, 1) {
		t.Fatalf("unexpected thumbnail bounds: %v", thumbnail.Bounds())
	}
}
