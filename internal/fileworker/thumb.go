package fileworker

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	_ "image/gif" // decodificadores registrados para image.Decode
	"image/jpeg"
	_ "image/png"
	"io"
)

// maxPixels limita la memoria al decodificar (≈ 50 MP ⇒ 200 MB RGBA como peor caso).
const maxPixels = 50_000_000

// MakeThumb decodifica una imagen JPEG/PNG/GIF y devuelve una miniatura JPEG cuyo lado
// mayor mide size (128 o 256) píxeles. Go nativo, sin librerías externas (ADR 0009).
func MakeThumb(r io.Reader, size int) ([]byte, error) {
	if size != 128 && size != 256 {
		size = 256
	}
	var head bytes.Buffer
	cfg, _, err := image.DecodeConfig(io.TeeReader(r, &head))
	if err != nil {
		return nil, errors.New("formato de imagen no soportado")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxPixels {
		return nil, errors.New("imagen demasiado grande")
	}
	img, _, err := image.Decode(io.MultiReader(&head, r))
	if err != nil {
		return nil, errors.New("imagen dañada")
	}
	out := Downscale(img, size)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: 80}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Downscale reduce img para que su lado mayor sea size, promediando (filtro de caja)
// todos los píxeles de origen que caen en cada píxel de destino. Si ya es más chica,
// solo la convierte a RGBA.
func Downscale(img image.Image, size int) *image.RGBA {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dw, dh := sw, sh
	if sw >= sh && sw > size {
		dw, dh = size, max(1, sh*size/sw)
	} else if sh > sw && sh > size {
		dw, dh = max(1, sw*size/sh), size
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		y0 := b.Min.Y + y*sh/dh
		y1 := max(y0+1, b.Min.Y+(y+1)*sh/dh)
		for x := 0; x < dw; x++ {
			x0 := b.Min.X + x*sw/dw
			x1 := max(x0+1, b.Min.X+(x+1)*sw/dw)
			var r, g, bl, a, n uint64
			// Para imágenes enormes se muestrea con paso: alcanza para una miniatura.
			step := max(1, (x1-x0)*(y1-y0)/64)
			i := 0
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					i++
					if i%step != 0 {
						continue
					}
					cr, cg, cb, ca := img.At(xx, yy).RGBA()
					r, g, bl, a = r+uint64(cr), g+uint64(cg), bl+uint64(cb), a+uint64(ca)
					n++
				}
			}
			if n == 0 {
				continue
			}
			dst.SetRGBA(x, y, color.RGBA{R: uint8(r / n >> 8), G: uint8(g / n >> 8), B: uint8(bl / n >> 8), A: uint8(a / n >> 8)})
		}
	}
	return dst
}
