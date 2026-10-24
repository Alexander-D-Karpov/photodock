package develop

import "testing"

func TestMergeModulesCopiesOnlySelected(t *testing.T) {
	dst := Recipe{Tone: Tone{Exposure: 1, Contrast: 10}, Geometry: Geometry{Rotate: 90}}
	src := Recipe{Tone: Tone{Exposure: -1, Curve: []Point{{0, 0.1}, {1, 1}}}, Local: Local{Clarity: 20}}
	got, err := MergeModules(dst, src, []string{"exposure", "tone", "local"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Tone.Exposure != -1 || got.Tone.Contrast != 0 {
		t.Errorf("tone module not replaced: %+v", got.Tone)
	}
	if got.Tone.Curve != nil {
		t.Error("curve belongs to its own module and must be kept from dst (nil)")
	}
	if got.Local.Clarity != 20 || got.Geometry.Rotate != 90 {
		t.Errorf("merge result %+v", got)
	}
}

func TestToneModuleKeepsExposure(t *testing.T) {
	got, err := MergeModules(Recipe{Tone: Tone{Exposure: 1}}, Recipe{Tone: Tone{Contrast: 20}}, []string{"tone"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Tone.Exposure != 1 || got.Tone.Contrast != 20 {
		t.Errorf("tone module must not touch exposure: %+v", got.Tone)
	}
}

func TestMergeModulesRejectsUnknown(t *testing.T) {
	if _, err := MergeModules(Recipe{}, Recipe{}, []string{"nope"}); err == nil {
		t.Error("unknown module must fail")
	}
	if _, err := MergeModules(Recipe{}, Recipe{}, nil); err == nil {
		t.Error("empty module list must fail")
	}
}

func TestPickModules(t *testing.T) {
	r := Recipe{WB: WBParams{Mode: WBCustom, Temp: 4000}, Tone: Tone{Exposure: 0.5}}
	p, err := PickModules(r, []string{"wb"})
	if err != nil {
		t.Fatal(err)
	}
	if p.WB.Temp != 4000 || p.Tone.Exposure != 0 {
		t.Errorf("picked %+v", p)
	}
	if !sameRecipe(p, p) || sameRecipe(p, r) {
		t.Error("sameRecipe broken")
	}
}

func TestMergeMasksModule(t *testing.T) {
	src := Recipe{Masks: []Mask{{ID: "a", Type: "radial", RX: 0.1, RY: 0.1, Adjust: MaskAdjust{Exposure: 1}}}}
	got, err := MergeModules(Recipe{}, src, []string{"masks"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Masks) != 1 || got.Masks[0].ID != "a" {
		t.Errorf("masks = %+v", got.Masks)
	}
	cleared, _ := MergeModules(got, Recipe{}, []string{"masks"})
	if len(cleared.Masks) != 0 {
		t.Error("merging an empty masks module must clear masks")
	}
}
