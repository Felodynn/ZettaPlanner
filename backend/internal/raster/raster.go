// Package raster turns an upload into the page images a vision model is shown.
//
// It exists as its own package because none of this is business logic and all
// of it is fiddly: a phone photograph arrives rotated with the rotation recorded
// only in EXIF, a PDF arrives as vectors that no model can see, and a 12
// megapixel image arrives as eight megabytes of base64 that buys no accuracy
// over two.
//
// The uploaded bytes are never modified. Everything here writes new files
// alongside the original, which is kept forever — when an extraction turns out
// to be wrong, the input is the evidence.
package raster

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"

	// Decoders for every format Sniff admits. Registered here rather than
	// wherever an image is first decoded, so that accepting a format and being
	// able to read it are the same edit.
	_ "image/gif"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"

	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"
)

// Limits on what one ingest may be.
const (
	// MaxLongEdge is the longest side of a page handed to the model.
	//
	// 2000px. Below about 1500 handwriting starts to fail; above about 2500 the
	// extra pixels are re-tiled by the model into the same patches and cost
	// tokens for nothing. Sized for a phone photograph of an A4 notebook page,
	// which is the actual input this was built for.
	MaxLongEdge = 2000

	// MaxPages is the ceiling on one ingest. A 300-page PDF is not a diagram.
	MaxPages = 40

	// PDFDensity is the rasterization resolution before downscaling. 200 DPI
	// renders a hand-annotated A4 at roughly 1650x2340, which MaxLongEdge then
	// trims — rendering straight to the target loses thin ink strokes.
	PDFDensity = 200

	// rasterTimeout bounds a pdftoppm run. A malformed PDF can otherwise hold
	// a worker for as long as the process lives.
	rasterTimeout = 2 * time.Minute
)

// ErrUnsupported is returned for bytes that are neither an image nor a PDF.
var ErrUnsupported = errors.New("unsupported file type")

// ErrNoRasterizer reports that PDF support is not installed.
var ErrNoRasterizer = errors.New("no PDF rasterizer")

// Kind is what a set of uploaded bytes turned out to be.
type Kind string

const (
	KindImage Kind = "image"
	KindPDF   Kind = "pdf"
)

// Page is one normalized image on disk.
type Page struct {
	Idx  int
	Path string
	// SourceIdx is which uploaded file this page came from, so a review screen
	// can say "page 2 of notebook-back.jpg" rather than only "page 2".
	SourceIdx int
	Width     int
	Height    int
}

// Sniff identifies bytes by their magic number.
//
// The client's Content-Type is not consulted. It is trivially wrong (browsers
// send application/octet-stream for anything they do not recognise) and
// trivially forged, and the consequence of believing it is handing a PDF to an
// image decoder and reporting "not an image" about a file that is fine.
func Sniff(b []byte) (Kind, string, error) {
	switch {
	case len(b) >= 4 && string(b[:4]) == "%PDF":
		return KindPDF, "pdf", nil
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return KindImage, "jpg", nil
	case len(b) >= 8 && string(b[1:4]) == "PNG":
		return KindImage, "png", nil
	case len(b) >= 6 && (string(b[:6]) == "GIF87a" || string(b[:6]) == "GIF89a"):
		return KindImage, "gif", nil
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return KindImage, "webp", nil
	case len(b) >= 2 && b[0] == 0x42 && b[1] == 0x4D:
		return KindImage, "bmp", nil
	}
	return "", "", fmt.Errorf("%w: the file is neither an image (JPEG, PNG, GIF, WebP, BMP) nor a PDF", ErrUnsupported)
}

// Options tune a conversion.
type Options struct {
	// Dir is where page images are written.
	Dir string
	// StartIdx is the page index the first produced page takes, so several
	// uploaded files can share one dense sequence.
	StartIdx int
	// SourceIdx identifies the uploaded file being converted.
	SourceIdx int
}

// Convert turns one uploaded file into page images.
func Convert(ctx context.Context, path string, kind Kind, opts Options) ([]Page, error) {
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating the page directory: %w", err)
	}
	if kind == KindPDF {
		return rasterizePDF(ctx, path, opts)
	}
	page, err := normalizeImage(path, opts, opts.StartIdx)
	if err != nil {
		return nil, err
	}
	return []Page{page}, nil
}

// ── PDF ──────────────────────────────────────────────────────────────────────

// rasterizePDF renders every page to PNG with pdftoppm.
//
// Shelling out to poppler rather than linking a Go PDF renderer: the pure-Go
// options render text and vectors but not the embedded scans that a
// photographed-then-PDF'd notebook actually contains, and a renderer that
// silently produces a blank page for a scan is the worst possible failure here.
func rasterizePDF(ctx context.Context, path string, opts Options) ([]Page, error) {
	bin, err := exec.LookPath("pdftoppm")
	if err != nil {
		return nil, fmt.Errorf("%w: pdftoppm is not on PATH. Install poppler-utils (apt install poppler-utils) to ingest PDFs; images work without it", ErrNoRasterizer)
	}

	ctx, cancel := context.WithTimeout(ctx, rasterTimeout)
	defer cancel()

	stem := filepath.Join(opts.Dir, fmt.Sprintf("src%02d", opts.SourceIdx))
	cmd := exec.CommandContext(ctx, bin,
		"-png",
		"-r", fmt.Sprintf("%d", PDFDensity),
		"-l", fmt.Sprintf("%d", MaxPages),
		path, stem,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("rasterizing the PDF failed: %s", detail)
	}

	produced, err := filepath.Glob(stem + "-*.png")
	if err != nil {
		return nil, err
	}
	if len(produced) == 0 {
		return nil, fmt.Errorf("the PDF produced no pages; it may be empty or encrypted")
	}
	// pdftoppm zero-pads its suffix, so lexical order is page order.
	sort.Strings(produced)

	out := make([]Page, 0, len(produced))
	for i, raw := range produced {
		idx := opts.StartIdx + i
		page, err := normalizeImage(raw, opts, idx)
		if err != nil {
			return nil, fmt.Errorf("normalizing rasterized page %d: %w", idx+1, err)
		}
		// The intermediate render is replaced by the normalized page. The
		// original PDF is what gets kept; a second copy of every page at full
		// rasterization size is disk for nothing.
		if page.Path != raw {
			_ = os.Remove(raw)
		}
		out = append(out, page)
	}
	return out, nil
}

// ── images ───────────────────────────────────────────────────────────────────

// normalizeImage decodes, rights and downscales one image.
func normalizeImage(path string, opts Options, idx int) (Page, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Page{}, err
	}

	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return Page{}, fmt.Errorf("decoding the image: %w", err)
	}

	// EXIF first, then scale. A phone photograph of a notebook is very often
	// stored landscape with a rotation flag, and Go's decoders ignore the flag.
	// A sideways page is not a page a vision model reads badly — it is a page
	// it reads as a different drawing.
	if format == "jpeg" {
		if o := exifOrientation(raw); o > 1 {
			img = applyOrientation(img, o)
		}
	}
	img = downscale(img, MaxLongEdge)

	ext := "png"
	if format == "jpeg" {
		// Line art keeps its edges better as PNG, but a photograph re-encoded
		// as PNG is five times the bytes for no gain in what the model can see.
		ext = "jpg"
	}
	out := filepath.Join(opts.Dir, fmt.Sprintf("page-%04d.%s", idx+1, ext))

	f, err := os.Create(out)
	if err != nil {
		return Page{}, err
	}
	defer f.Close()

	if ext == "jpg" {
		err = jpeg.Encode(f, img, &jpeg.Options{Quality: 92})
	} else {
		err = (&png.Encoder{CompressionLevel: png.DefaultCompression}).Encode(f, img)
	}
	if err != nil {
		return Page{}, fmt.Errorf("encoding the normalized page: %w", err)
	}

	b := img.Bounds()
	return Page{Idx: idx, Path: out, SourceIdx: opts.SourceIdx, Width: b.Dx(), Height: b.Dy()}, nil
}

// downscale shrinks an image so its longest side is at most max. It never
// enlarges: upscaling a blurred photograph adds pixels and no information.
func downscale(src image.Image, max int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= max && h <= max {
		return src
	}
	scale := float64(max) / float64(w)
	if h > w {
		scale = float64(max) / float64(h)
	}
	dst := image.NewRGBA(image.Rect(0, 0, int(float64(w)*scale), int(float64(h)*scale)))
	// CatmullRom rather than a box filter: pen strokes one or two pixels wide
	// disappear entirely under naive averaging, and those strokes are the
	// arrows.
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Over, nil)
	return dst
}

// applyOrientation rights an image according to its EXIF orientation tag.
func applyOrientation(src image.Image, orientation int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()

	// Orientations 5-8 exchange the axes.
	dstRect := image.Rect(0, 0, w, h)
	if orientation >= 5 {
		dstRect = image.Rect(0, 0, h, w)
	}
	dst := image.NewRGBA(dstRect)

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := src.At(b.Min.X+x, b.Min.Y+y)
			var dx, dy int
			switch orientation {
			case 2: // mirrored horizontally
				dx, dy = w-1-x, y
			case 3: // rotated 180
				dx, dy = w-1-x, h-1-y
			case 4: // mirrored vertically
				dx, dy = x, h-1-y
			case 5: // mirrored horizontally then rotated 270 clockwise
				dx, dy = y, x
			case 6: // rotated 90 clockwise — by far the most common
				dx, dy = h-1-y, x
			case 7: // mirrored horizontally then rotated 90 clockwise
				dx, dy = h-1-y, w-1-x
			case 8: // rotated 270 clockwise
				dx, dy = y, w-1-x
			default:
				dx, dy = x, y
			}
			dst.Set(dx, dy, c)
		}
	}
	return dst
}

// exifOrientation reads tag 0x0112 out of a JPEG's APP1 segment.
//
// Hand-rolled rather than pulled in as a dependency because one tag is all that
// is wanted: a full EXIF parser is a large amount of code, and most of it
// concerns metadata that has no business being anywhere near a model prompt.
// Returns 0 when there is no orientation to apply, including on anything
// malformed — a photograph with a corrupt EXIF block is still a photograph.
func exifOrientation(jpegBytes []byte) int {
	const (
		app1   = 0xE1
		soi    = 0xD8
		sos    = 0xDA
		tagOri = 0x0112
	)
	if len(jpegBytes) < 4 || jpegBytes[0] != 0xFF || jpegBytes[1] != soi {
		return 0
	}

	// Walk the segment chain looking for APP1/Exif.
	for i := 2; i+4 <= len(jpegBytes); {
		if jpegBytes[i] != 0xFF {
			return 0
		}
		marker := jpegBytes[i+1]
		if marker == sos {
			return 0 // image data begins; no EXIF found
		}
		size := int(binary.BigEndian.Uint16(jpegBytes[i+2 : i+4]))
		if size < 2 || i+2+size > len(jpegBytes) {
			return 0
		}
		segment := jpegBytes[i+4 : i+2+size]
		if marker == app1 && len(segment) > 6 && string(segment[:6]) == "Exif\x00\x00" {
			return orientationFromTIFF(segment[6:], tagOri)
		}
		i += 2 + size
	}
	return 0
}

// orientationFromTIFF reads one short tag out of IFD0 of a TIFF header.
func orientationFromTIFF(tiff []byte, want uint16) int {
	if len(tiff) < 8 {
		return 0
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 0
	}

	offset := int(order.Uint32(tiff[4:8]))
	if offset < 8 || offset+2 > len(tiff) {
		return 0
	}
	count := int(order.Uint16(tiff[offset : offset+2]))
	entries := offset + 2

	for i := 0; i < count; i++ {
		e := entries + i*12
		if e+12 > len(tiff) {
			return 0
		}
		if order.Uint16(tiff[e:e+2]) != want {
			continue
		}
		// A SHORT value sits in the first two bytes of the value field.
		v := int(order.Uint16(tiff[e+8 : e+10]))
		if v < 1 || v > 8 {
			return 0
		}
		return v
	}
	return 0
}
