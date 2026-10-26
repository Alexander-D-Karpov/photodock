package develop

import (
	"encoding/json"
	"fmt"
	"strings"
)

// moduleOrder is the canonical module order (presets list modules in it).
var moduleOrder = []string{
	"wb", "exposure", "tone", "curve", "local", "presence", "hsl", "grading", "bw", "mixer",
	"sharpen", "noise", "lens", "raw", "crop", "masks",
}

// modulePaths maps each module to the recipe JSON paths it owns; must match recipe.js MODULE_GROUPS.
var modulePaths = map[string][]string{
	"wb":       {"wb"},
	"exposure": {"tone.exposure"},
	"tone":     {"tone.contrast", "tone.highlights", "tone.shadows", "tone.whites", "tone.blacks"},
	"curve":    {"tone.curve"},
	"local":    {"local"},
	"presence": {"color.vibrance", "color.saturation"},
	"hsl":      {"color.hsl"},
	"grading":  {"color.grading"},
	"bw":       {"color.bw"},
	"mixer":    {"color.mixer"},
	"sharpen":  {"detail.sharpen"},
	"noise":    {"detail.noise"},
	"lens":     {"lens"},
	"raw":      {"raw"},
	"crop":     {"geometry"},
	"masks":    {"masks"},
}

// PresetModules lists, in moduleOrder, the modules a recipe actually sets.
func PresetModules(r Recipe) []string {
	m, err := recipeMap(r)
	if err != nil {
		return nil
	}
	var out []string
	for _, mod := range moduleOrder {
		for _, p := range modulePaths[mod] {
			if v, ok := lookupPath(m, p); ok && prune(v) != nil {
				out = append(out, mod)
				break
			}
		}
	}
	return out
}

func ValidModules(modules []string) error {
	if len(modules) == 0 {
		return fmt.Errorf("%w: no modules selected", ErrInvalidRecipe)
	}
	for _, m := range modules {
		if _, ok := modulePaths[m]; !ok {
			return fmt.Errorf("%w: unknown module %q", ErrInvalidRecipe, m)
		}
	}
	return nil
}

func recipeMap(r Recipe) (map[string]any, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	return m, json.Unmarshal(b, &m)
}

func recipeFromMap(m map[string]any) (Recipe, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return Recipe{}, err
	}
	return ParseRecipe(b)
}

func lookupPath(m map[string]any, path string) (any, bool) {
	var cur any = m
	for _, p := range strings.Split(path, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = mm[p]; !ok {
			return nil, false
		}
	}
	return cur, true
}

func parentOf(m map[string]any, path string, create bool) (map[string]any, string) {
	parts := strings.Split(path, ".")
	cur := m
	for _, p := range parts[:len(parts)-1] {
		next, ok := cur[p].(map[string]any)
		if !ok {
			if !create {
				return nil, ""
			}
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
	return cur, parts[len(parts)-1]
}

// MergeModules returns dst with the given modules' paths replaced by src's values (removed if absent).
func MergeModules(dst, src Recipe, modules []string) (Recipe, error) {
	if err := ValidModules(modules); err != nil {
		return Recipe{}, err
	}
	dm, err := recipeMap(dst)
	if err != nil {
		return Recipe{}, err
	}
	sm, err := recipeMap(src)
	if err != nil {
		return Recipe{}, err
	}
	for _, mod := range modules {
		for _, path := range modulePaths[mod] {
			if v, ok := lookupPath(sm, path); ok {
				parent, key := parentOf(dm, path, true)
				parent[key] = v
			} else if parent, key := parentOf(dm, path, false); parent != nil {
				delete(parent, key)
			}
		}
	}
	return recipeFromMap(dm)
}

func PickModules(r Recipe, modules []string) (Recipe, error) {
	return MergeModules(Recipe{}, r, modules)
}

func sameRecipe(a, b Recipe) bool {
	ca, err1 := canonicalJSON(a)
	cb, err2 := canonicalJSON(b)
	return err1 == nil && err2 == nil && string(ca) == string(cb)
}
