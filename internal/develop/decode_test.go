package develop

import "testing"

// Trimmed `raw-identify -v` output from LibRaw 0.22 for a Sony ILCE-6000 ARW.
const rawIdentify022 = `Camera: Sony ILCE-6000 ID: 0x138
Image flip: 5
Makernotes WB data:               coeffs                  EVs
  As shot                   2720 1024 1824 1024    1.41  0.00  0.83  0.00
  Daylight                  2716 1024 1824 1024    1.41  0.00  0.83  0.00
  Tungsten (Incandescent)   1712 1024 3284 1024    0.74  0.00  1.68  0.00
  Daylight Fluorescent      2924 1024 1748 1024    1.51  0.00  0.77  0.00

Derived D65 multipliers: 2.671762 0.921517 1.242349
`

func TestParseRawIdentifyMakernotesTable(t *testing.T) {
	asShot, daylight := parseRawIdentify(rawIdentify022)
	if asShot != [3]float64{2720, 1024, 1824} {
		t.Errorf("as shot = %v", asShot)
	}
	if daylight != [3]float64{2716, 1024, 1824} {
		t.Errorf("daylight = %v", daylight)
	}
}

func TestParseRawIdentifyLegacyLines(t *testing.T) {
	asShot, daylight := parseRawIdentify("Camera multipliers: 2.5 1 1.6 1\nDaylight multipliers: 2.4 1 1.5\n")
	if asShot != [3]float64{2.5, 1, 1.6} || daylight != [3]float64{2.4, 1, 1.5} {
		t.Errorf("as shot %v, daylight %v", asShot, daylight)
	}
}

func TestParseRawIdentifyDerivedFallback(t *testing.T) {
	asShot, daylight := parseRawIdentify("Derived D65 multipliers: 2.671762 0.921517 1.242349\n")
	if validMul(asShot) {
		t.Errorf("as shot must be empty, got %v", asShot)
	}
	if daylight != [3]float64{2.671762, 0.921517, 1.242349} {
		t.Errorf("daylight = %v", daylight)
	}
}
