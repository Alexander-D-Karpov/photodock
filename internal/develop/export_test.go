package develop

import (
	"bytes"
	"image"
	"math"
	"testing"

	"golang.org/x/image/tiff"
)

func TestICCRoundTrip(t *testing.T) {
	for name, c := range map[string]struct {
		icc  []byte
		want Mat3
	}{
		"srgb": {iccSRGB, rec2020FromSRGB},
		"p3":   {iccP3, rec2020FromXYZ.Mul(xyzFromP3)},
	} {
		p, err := parseICC(c.icc)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for i := 0; i < 3; i++ {
			for j := 0; j < 3; j++ {
				if math.Abs(p.toRec2020[i][j]-c.want[i][j]) > 2e-3 {
					t.Errorf("%s: m[%d][%d] = %v; want %v", name, i, j, p.toRec2020[i][j], c.want[i][j])
				}
			}
		}
		if math.Abs(float64(p.lut[0][128])-srgbDecode(128.0/255)) > 1e-3 {
			t.Errorf("%s: TRC mismatch", name)
		}
	}
}

func TestJPEGCarriesICC(t *testing.T) {
	img := NewImage(8, 8)
	var buf bytes.Buffer
	if err := writeJPEG(&buf, toSRGB8(img), 90, iccSRGB); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(extractICC(buf.Bytes()), iccSRGB) {
		t.Error("ICC not embedded")
	}
	if _, _, err := image.Decode(bytes.NewReader(buf.Bytes())); err != nil {
		t.Errorf("jpeg not decodable: %v", err)
	}
}

func TestTIFF16Writer(t *testing.T) {
	img := NewImage(5, 3)
	for i := range img.Pix {
		img.Pix[i] = 0.18
	}
	var buf bytes.Buffer
	if err := writeTIFF16(&buf, img, srgbFromRec2020, iccSRGB); err != nil {
		t.Fatal(err)
	}
	dec, err := tiff.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if dec.Bounds().Dx() != 5 || dec.Bounds().Dy() != 3 {
		t.Fatalf("dims %v", dec.Bounds())
	}
	r, g, b, _ := dec.At(2, 1).RGBA()
	want := srgbEncode(0.18) * 65535
	for _, v := range []uint32{r, g, b} {
		if math.Abs(float64(v)-want) > 40 {
			t.Errorf("channel = %d; want ~%.0f", v, want)
		}
	}
}

func TestExportOptionsNormalize(t *testing.T) {
	o := ExportOptions{}
	if err := o.normalize(); err != nil || o.Format != "jpeg" || o.Space != "srgb" || o.Quality != 92 {
		t.Errorf("defaults = %+v, %v", o, err)
	}
	if err := (&ExportOptions{Format: "png"}).normalize(); err == nil {
		t.Error("png must be rejected")
	}
}
