package develop

import (
	"errors"
	"testing"
)

func TestNormalizeClamps(t *testing.T) {
	r, err := Recipe{
		WB:       WBParams{Mode: "CUSTOM", Temp: 90000, Tint: -900},
		Geometry: Geometry{Rotate: -90},
		Tone:     Tone{Exposure: 12.345},
	}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if r.WB.Mode != WBCustom || r.WB.Temp != MaxTemp || r.WB.Tint != -MaxTint {
		t.Errorf("wb = %+v", r.WB)
	}
	if r.Geometry.Rotate != 270 {
		t.Errorf("rotate = %d; want 270", r.Geometry.Rotate)
	}
	if r.Tone.Exposure != 5 {
		t.Errorf("exposure = %v; want 5", r.Tone.Exposure)
	}
}

func TestNormalizeRejectsUnknown(t *testing.T) {
	for _, r := range []Recipe{
		{Raw: RawParams{Demosaic: "amaze"}},
		{Raw: RawParams{Highlights: "magic"}},
		{WB: WBParams{Mode: "auto"}},
		{WB: WBParams{Mode: WBCustom}},
	} {
		if _, err := r.Normalize(); !errors.Is(err, ErrInvalidRecipe) {
			t.Errorf("%+v: err = %v; want ErrInvalidRecipe", r, err)
		}
	}
}

func TestAsShotDropsTempTint(t *testing.T) {
	r, err := Recipe{WB: WBParams{Mode: WBAsShot, Temp: 3000, Tint: 10}}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if r.WB.Temp != 0 || r.WB.Tint != 0 {
		t.Errorf("as_shot kept temp/tint: %+v", r.WB)
	}
}

func TestFullCropIsNil(t *testing.T) {
	r, err := Recipe{Geometry: Geometry{Crop: &Rect{X: 0, Y: 0, W: 1, H: 1}}}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if r.Geometry.Crop != nil {
		t.Errorf("full crop not dropped: %+v", r.Geometry.Crop)
	}
}

func TestCropClampedInside(t *testing.T) {
	r, _ := Recipe{Geometry: Geometry{Crop: &Rect{X: 0.5, Y: -1, W: 2, H: 0}}}.Normalize()
	c := r.Geometry.Crop
	if c == nil || c.X != 0.5 || c.Y != 0 || c.W > 0.5+1e-9 || c.H < 0.01 {
		t.Errorf("crop = %+v", c)
	}
}

func TestResolveFillsDefaults(t *testing.T) {
	r, err := Resolve(Recipe{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if r.Raw.Demosaic != "aahd" || r.Raw.Highlights != "clip" || r.WB.Mode != WBAsShot {
		t.Errorf("resolved = %+v", r)
	}
	if _, err := Resolve(Recipe{}, 999); err == nil {
		t.Error("unknown defaults version must fail")
	}
}

func TestResolveKeepsExplicit(t *testing.T) {
	r, _ := Resolve(Recipe{Raw: RawParams{Demosaic: "dcb"}}, 1)
	if r.Raw.Demosaic != "dcb" {
		t.Errorf("demosaic = %q; want dcb", r.Raw.Demosaic)
	}
}

func TestHashIgnoresZeroValues(t *testing.T) {
	a, _ := Resolve(Recipe{}, 1)
	b, _ := Resolve(Recipe{Tone: Tone{Exposure: 0}, Geometry: Geometry{FlipH: false, Rotate: 0}}, 1)
	ha, _ := RenderHash("fp", a, "lr")
	hb, _ := RenderHash("fp", b, "lr")
	if ha != hb {
		t.Errorf("zero fields changed hash: %s vs %s", ha, hb)
	}
	if len(ha) != 16 {
		t.Errorf("hash length = %d; want 16", len(ha))
	}
}

func TestHashSensitivity(t *testing.T) {
	base, _ := Resolve(Recipe{}, 1)
	h0, _ := RenderHash("fp", base, "lr")

	exp := base
	exp.Tone.Exposure = 0.5
	cases := map[string]struct {
		fp, lr string
		r      Recipe
	}{
		"exposure": {"fp", "lr", exp},
		"source":   {"fp2", "lr", base},
		"libraw":   {"fp", "lr2", base},
	}
	for name, c := range cases {
		h, _ := RenderHash(c.fp, c.r, c.lr)
		if h == h0 {
			t.Errorf("%s change did not change hash", name)
		}
	}
}

func TestHashStableAcrossCalls(t *testing.T) {
	r, _ := Resolve(Recipe{
		WB:       WBParams{Mode: WBCustom, Temp: 5200, Tint: 4},
		Geometry: Geometry{Rotate: 90, Crop: &Rect{X: 0.1, Y: 0.1, W: 0.5, H: 0.5}},
	}, 1)
	first, _ := RenderHash("fp", r, "lr")
	for i := 0; i < 50; i++ {
		if h, _ := RenderHash("fp", r, "lr"); h != first {
			t.Fatalf("hash not deterministic: %s vs %s", h, first)
		}
	}
}

func TestParseRecipeIgnoresUnknownFields(t *testing.T) {
	r, err := ParseRecipe([]byte(`{"tone":{"exposure":1,"future":3},"lens":{"x":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	if r.Tone.Exposure != 1 {
		t.Errorf("exposure = %v", r.Tone.Exposure)
	}
	if _, err := ParseRecipe([]byte(`null`)); err != nil {
		t.Errorf("null recipe: %v", err)
	}
	if _, err := ParseRecipe([]byte(`{bad`)); !errors.Is(err, ErrInvalidRecipe) {
		t.Errorf("bad json err = %v", err)
	}
}
