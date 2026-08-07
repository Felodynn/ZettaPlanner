package raster

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSniffReadsMagicNumbersNotTheClientsClaim(t *testing.T) {
	cases := []struct {
		name string
		head []byte
		want Kind
		ext  string
	}{
		{"pdf", []byte("%PDF-1.7\n"), KindPDF, "pdf"},
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0}, KindImage, "jpg"},
		{"png", []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, KindImage, "png"},
		{"gif", []byte("GIF89a..."), KindImage, "gif"},
		{"webp", append([]byte("RIFF\x00\x00\x00\x00"), []byte("WEBP")...), KindImage, "webp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, ext, err := Sniff(tc.head)
			if err != nil {
				t.Fatalf("Sniff: %v", err)
			}
			if kind != tc.want || ext != tc.ext {
				t.Errorf("got %s/%s, want %s/%s", kind, ext, tc.want, tc.ext)
			}
		})
	}

	// A .jpg that is actually a zip is a .zip. The message has to say what was
	// wanted, because the user's next move is to convert the file.
	if _, _, err := Sniff([]byte("PK\x03\x04something")); err == nil {
		t.Fatal("a zip must not sniff as an image")
	}
}

// TestARotatedPhotographIsRighted is the one that matters for the manual test:
// a phone stores a portrait photograph landscape with the rotation only in
// EXIF, Go's decoder ignores the flag, and a sideways page is not a page a
// model reads badly — it is a page it reads as a different drawing.
func TestARotatedPhotographIsRighted(t *testing.T) {
	dir := t.TempDir()

	// 40x20 landscape with a red block in the top-left corner. A block rather
	// than a pixel: JPEG works in 8x8 blocks and would smear a lone pixel into
	// the background, which would make this test fail for a reason that has
	// nothing to do with rotation.
	src := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			src.Set(x, y, color.White)
		}
	}
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			src.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	// Orientation 6: rotated 90° clockwise, by far the most common.
	withExif := injectOrientation(t, buf.Bytes(), 6)

	path := filepath.Join(dir, "photo.jpg")
	if err := os.WriteFile(path, withExif, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := exifOrientation(withExif); got != 6 {
		t.Fatalf("exifOrientation = %d, want 6", got)
	}

	page, err := normalizeImage(path, Options{Dir: dir}, 0)
	if err != nil {
		t.Fatalf("normalizeImage: %v", err)
	}
	if page.Width != 20 || page.Height != 40 {
		t.Fatalf("normalized to %dx%d, want the axes exchanged (20x40)", page.Width, page.Height)
	}

	// Under a 90° clockwise rotation the old top-left corner lands at the
	// top-right. Sampled inside the block rather than at its edge.
	out := decode(t, page.Path)
	if got := out.At(15, 5); !reddish(got) {
		t.Errorf("the corner marker did not rotate to the top-right; got %v", got)
	}
	if got := out.At(5, 35); reddish(got) {
		t.Errorf("the marker is still at the bottom-left, so nothing rotated: %v", got)
	}
}

func TestAnImageWithNoOrientationIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	src := image.NewRGBA(image.Rect(0, 0, 30, 10))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "flat.jpg")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := exifOrientation(buf.Bytes()); got != 0 {
		t.Errorf("exifOrientation = %d, want 0 for a file with no EXIF", got)
	}
	page, err := normalizeImage(path, Options{Dir: dir}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Width != 30 || page.Height != 10 {
		t.Errorf("normalized to %dx%d, want 30x10 unchanged", page.Width, page.Height)
	}
}

func TestCorruptExifIsNotAFailedUpload(t *testing.T) {
	// A photograph with a mangled EXIF block is still a photograph. Anything
	// unreadable here returns "no orientation" rather than an error.
	for _, bad := range [][]byte{
		{},
		{0xFF, 0xD8},
		{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x02},
		append([]byte{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x10}, []byte("Exif\x00\x00garbage")...),
	} {
		if got := exifOrientation(bad); got != 0 {
			t.Errorf("exifOrientation(% x) = %d, want 0", bad, got)
		}
	}
}

func TestALargeImageIsDownscaledAndASmallOneIsNot(t *testing.T) {
	big := image.NewRGBA(image.Rect(0, 0, 5000, 2500))
	out := downscale(big, MaxLongEdge)
	if out.Bounds().Dx() != MaxLongEdge {
		t.Errorf("long edge = %d, want %d", out.Bounds().Dx(), MaxLongEdge)
	}
	if out.Bounds().Dy() != MaxLongEdge/2 {
		t.Errorf("aspect ratio not preserved: %v", out.Bounds())
	}

	// Upscaling a blurred photograph adds pixels and no information.
	small := image.NewRGBA(image.Rect(0, 0, 400, 300))
	if got := downscale(small, MaxLongEdge); got.Bounds() != small.Bounds() {
		t.Errorf("a small image was resized to %v", got.Bounds())
	}
}

func TestAPDFBecomesOnePagePerPage(t *testing.T) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Skip("pdftoppm not installed; PDF ingest is unavailable on this machine")
	}
	src := writeTestPDF(t, 3)

	dir := t.TempDir()
	pages, err := Convert(context.Background(), src, KindPDF, Options{Dir: dir})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(pages) != 3 {
		t.Fatalf("got %d pages, want 3", len(pages))
	}
	for i, p := range pages {
		if p.Idx != i {
			t.Errorf("page %d has index %d; indices must be dense and in order", i, p.Idx)
		}
		if _, err := os.Stat(p.Path); err != nil {
			t.Errorf("page %d was not written: %v", i, err)
		}
		if p.Width > MaxLongEdge || p.Height > MaxLongEdge {
			t.Errorf("page %d is %dx%d, above the %d cap", i, p.Width, p.Height, MaxLongEdge)
		}
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func decode(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func reddish(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return r > 0x8000 && g < 0x8000 && b < 0x8000
}

// injectOrientation splices a minimal APP1/Exif segment carrying one tag into a
// JPEG, right after the SOI marker.
func injectOrientation(t *testing.T, jpegBytes []byte, orientation uint16) []byte {
	t.Helper()

	var tiff bytes.Buffer
	tiff.WriteString("MM")                                // big-endian
	_ = binary.Write(&tiff, binary.BigEndian, uint16(42)) // magic
	_ = binary.Write(&tiff, binary.BigEndian, uint32(8))  // IFD0 at offset 8
	_ = binary.Write(&tiff, binary.BigEndian, uint16(1))  // one entry
	_ = binary.Write(&tiff, binary.BigEndian, uint16(0x0112) /* Orientation */)
	_ = binary.Write(&tiff, binary.BigEndian, uint16(3))   // SHORT
	_ = binary.Write(&tiff, binary.BigEndian, uint32(1))   // count
	_ = binary.Write(&tiff, binary.BigEndian, orientation) // value, in the high half
	_ = binary.Write(&tiff, binary.BigEndian, uint16(0))   // padding
	_ = binary.Write(&tiff, binary.BigEndian, uint32(0))   // next IFD

	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)

	var out bytes.Buffer
	out.Write(jpegBytes[:2]) // SOI
	out.Write([]byte{0xFF, 0xE1})
	_ = binary.Write(&out, binary.BigEndian, uint16(len(payload)+2))
	out.Write(payload)
	out.Write(jpegBytes[2:])
	return out.Bytes()
}

// writeTestPDF emits a minimal multi-page PDF with a visible mark on each page.
func writeTestPDF(t *testing.T, pages int) string {
	t.Helper()

	var (
		buf     bytes.Buffer
		offsets []int
	)
	obj := func(body string) {
		offsets = append(offsets, buf.Len())
		buf.WriteString(body)
	}

	buf.WriteString("%PDF-1.4\n")

	kids := ""
	for i := 0; i < pages; i++ {
		kids += itoa(3+i*2) + " 0 R "
	}
	obj("1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n")
	obj("2 0 obj<</Type/Pages/Count " + itoa(pages) + "/Kids[" + kids + "]>>endobj\n")

	for i := 0; i < pages; i++ {
		pageNum, contentNum := 3+i*2, 4+i*2
		obj(itoa(pageNum) + " 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]" +
			"/Contents " + itoa(contentNum) + " 0 R>>endobj\n")
		stream := "100 600 200 100 re f\n"
		obj(itoa(contentNum) + " 0 obj<</Length " + itoa(len(stream)) + ">>stream\n" + stream + "endstream endobj\n")
	}

	xref := buf.Len()
	buf.WriteString("xref\n0 " + itoa(len(offsets)+1) + "\n0000000000 65535 f \n")
	for _, off := range offsets {
		buf.WriteString(pad10(off) + " 00000 n \n")
	}
	buf.WriteString("trailer<</Size " + itoa(len(offsets)+1) + "/Root 1 0 R>>\nstartxref\n" + itoa(xref) + "\n%%EOF\n")

	path := filepath.Join(t.TempDir(), "pages.pdf")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func pad10(n int) string {
	s := itoa(n)
	for len(s) < 10 {
		s = "0" + s
	}
	return s
}
