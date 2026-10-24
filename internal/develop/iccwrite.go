package develop

import (
	"bytes"
	"encoding/binary"
	"math"
)

// Minimal ICC v2.1 matrix/TRC display profiles for exported files.

var xyzFromP3 = Mat3{
	{0.4865709, 0.2656677, 0.1982173},
	{0.2289746, 0.6917385, 0.0792869},
	{0.0000000, 0.0451134, 1.0439444},
}

var (
	p3FromRec2020 = mustInverse(xyzFromP3).Mul(xyzFromRec2020)
	iccSRGB       = buildICC("sRGB IEC61966-2.1 (PhotoDock)", xyzFromSRGB)
	iccP3         = buildICC("Display P3 (PhotoDock)", xyzFromP3)
)

func s15Fixed16(v float64) uint32 {
	return uint32(int32(math.Round(v * 65536)))
}

func iccXYZ(v [3]float64) []byte {
	b := make([]byte, 20)
	copy(b, "XYZ ")
	for i := 0; i < 3; i++ {
		binary.BigEndian.PutUint32(b[8+4*i:], s15Fixed16(v[i]))
	}
	return b
}

func iccText(s string) []byte {
	b := make([]byte, 8, 9+len(s))
	copy(b, "text")
	b = append(b, s...)
	return append(b, 0)
}

func iccDesc(s string) []byte {
	var buf bytes.Buffer
	buf.WriteString("desc")
	buf.Write(make([]byte, 4))
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(s)+1))
	buf.WriteString(s)
	buf.WriteByte(0)
	buf.Write(make([]byte, 8+3+67))
	return buf.Bytes()
}

func iccCurve() []byte {
	const n = 1024
	b := make([]byte, 12+2*n)
	copy(b, "curv")
	binary.BigEndian.PutUint32(b[8:], n)
	for i := 0; i < n; i++ {
		binary.BigEndian.PutUint16(b[12+2*i:], uint16(math.Round(srgbDecode(float64(i)/(n-1))*65535)))
	}
	return b
}

func buildICC(desc string, toXYZD65 Mat3) []byte {
	m := CAT(whiteD65, whiteD50).Mul(toXYZD65)
	col := func(i int) [3]float64 { return [3]float64{m[0][i], m[1][i], m[2][i]} }
	curve := iccCurve()
	type tag struct {
		sig  string
		data []byte
	}
	tags := []tag{
		{"desc", iccDesc(desc)},
		{"cprt", iccText("No copyright, use freely")},
		{"wtpt", iccXYZ(whiteD50)},
		{"rXYZ", iccXYZ(col(0))},
		{"gXYZ", iccXYZ(col(1))},
		{"bXYZ", iccXYZ(col(2))},
		{"rTRC", curve},
		{"gTRC", curve},
		{"bTRC", curve},
	}
	tableEnd := 128 + 4 + 12*len(tags)
	var data bytes.Buffer
	offsets := make([]int, len(tags))
	curveOff := 0
	for i, t := range tags {
		// The three TRC tags share one curve.
		if t.sig == "gTRC" || t.sig == "bTRC" {
			offsets[i] = curveOff
			continue
		}
		for data.Len()%4 != 0 {
			data.WriteByte(0)
		}
		offsets[i] = tableEnd + data.Len()
		if t.sig == "rTRC" {
			curveOff = offsets[i]
		}
		data.Write(t.data)
	}
	for data.Len()%4 != 0 {
		data.WriteByte(0)
	}

	total := tableEnd + data.Len()
	out := make([]byte, total)
	be := binary.BigEndian
	be.PutUint32(out[0:], uint32(total))
	be.PutUint32(out[8:], 0x02100000)
	copy(out[12:], "mntr")
	copy(out[16:], "RGB ")
	copy(out[20:], "XYZ ")
	for i, v := range []uint16{2026, 1, 1, 0, 0, 0} {
		be.PutUint16(out[24+2*i:], v)
	}
	copy(out[36:], "acsp")
	for i := 0; i < 3; i++ {
		be.PutUint32(out[68+4*i:], s15Fixed16(whiteD50[i]))
	}
	be.PutUint32(out[128:], uint32(len(tags)))
	for i, t := range tags {
		o := 132 + 12*i
		copy(out[o:], t.sig)
		be.PutUint32(out[o+4:], uint32(offsets[i]))
		be.PutUint32(out[o+8:], uint32(len(t.data)))
	}
	copy(out[tableEnd:], data.Bytes())
	return out
}
