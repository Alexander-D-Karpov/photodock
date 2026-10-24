package develop

import (
	"errors"
	"math"
)

const (
	MinTemp   = 2000.0
	MaxTemp   = 25000.0
	MaxTint   = 150.0
	tintScale = 5000.0
)

type Mat3 [3][3]float64

var identity3 = Mat3{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}

var (
	whiteD65 = [3]float64{0.95047, 1, 1.08883}
	whiteD50 = [3]float64{0.96422, 1, 0.82521}

	xyzFromRec2020 = Mat3{
		{0.6369580, 0.1446169, 0.1688810},
		{0.2627002, 0.6779981, 0.0593017},
		{0.0000000, 0.0280727, 1.0609851},
	}
	xyzFromSRGB = Mat3{
		{0.4124564, 0.3575761, 0.1804375},
		{0.2126729, 0.7151522, 0.0721750},
		{0.0193339, 0.1191920, 0.9503041},
	}
	bradfordM = Mat3{
		{0.8951, 0.2664, -0.1614},
		{-0.7502, 1.7135, 0.0367},
		{0.0389, -0.0685, 1.0296},
	}

	rec2020FromXYZ  = mustInverse(xyzFromRec2020)
	srgbFromXYZ     = mustInverse(xyzFromSRGB)
	bradfordInv     = mustInverse(bradfordM)
	rec2020FromSRGB = rec2020FromXYZ.Mul(xyzFromSRGB)
	srgbFromRec2020 = srgbFromXYZ.Mul(xyzFromRec2020)
	d65FromD50      = CAT(whiteD50, whiteD65)
)

func (a Mat3) Mul(b Mat3) Mat3 {
	var m Mat3
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			for k := 0; k < 3; k++ {
				m[i][j] += a[i][k] * b[k][j]
			}
		}
	}
	return m
}

func (a Mat3) Apply(v [3]float64) [3]float64 {
	return [3]float64{
		a[0][0]*v[0] + a[0][1]*v[1] + a[0][2]*v[2],
		a[1][0]*v[0] + a[1][1]*v[1] + a[1][2]*v[2],
		a[2][0]*v[0] + a[2][1]*v[1] + a[2][2]*v[2],
	}
}

func (a Mat3) Inverse() (Mat3, bool) {
	det := a[0][0]*(a[1][1]*a[2][2]-a[1][2]*a[2][1]) -
		a[0][1]*(a[1][0]*a[2][2]-a[1][2]*a[2][0]) +
		a[0][2]*(a[1][0]*a[2][1]-a[1][1]*a[2][0])
	if math.Abs(det) < 1e-12 {
		return Mat3{}, false
	}
	inv := 1 / det
	var m Mat3
	m[0][0] = (a[1][1]*a[2][2] - a[1][2]*a[2][1]) * inv
	m[0][1] = (a[0][2]*a[2][1] - a[0][1]*a[2][2]) * inv
	m[0][2] = (a[0][1]*a[1][2] - a[0][2]*a[1][1]) * inv
	m[1][0] = (a[1][2]*a[2][0] - a[1][0]*a[2][2]) * inv
	m[1][1] = (a[0][0]*a[2][2] - a[0][2]*a[2][0]) * inv
	m[1][2] = (a[0][2]*a[1][0] - a[0][0]*a[1][2]) * inv
	m[2][0] = (a[1][0]*a[2][1] - a[1][1]*a[2][0]) * inv
	m[2][1] = (a[0][1]*a[2][0] - a[0][0]*a[2][1]) * inv
	m[2][2] = (a[0][0]*a[1][1] - a[0][1]*a[1][0]) * inv
	return m, true
}

func mustInverse(a Mat3) Mat3 {
	m, ok := a.Inverse()
	if !ok {
		panic("develop: singular matrix")
	}
	return m
}

func toMat32(m Mat3) [9]float32 {
	return [9]float32{
		float32(m[0][0]), float32(m[0][1]), float32(m[0][2]),
		float32(m[1][0]), float32(m[1][1]), float32(m[1][2]),
		float32(m[2][0]), float32(m[2][1]), float32(m[2][2]),
	}
}

func CAT(src, dst [3]float64) Mat3 {
	s := bradfordM.Apply(src)
	d := bradfordM.Apply(dst)
	diag := Mat3{{d[0] / s[0], 0, 0}, {0, d[1] / s[1], 0}, {0, 0, d[2] / s[2]}}
	return bradfordInv.Mul(diag).Mul(bradfordM)
}

func srgbDecode(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func srgbEncode(v float64) float64 {
	if v <= 0.0031308 {
		return v * 12.92
	}
	return 1.055*math.Pow(v, 1/2.4) - 0.055
}

func xyToXYZ(x, y float64) [3]float64 {
	return [3]float64{x / y, 1, (1 - x - y) / y}
}

func xyzToXY(v [3]float64) (float64, float64) {
	s := v[0] + v[1] + v[2]
	return v[0] / s, v[1] / s
}

func xyToUV(x, y float64) (float64, float64) {
	d := -2*x + 12*y + 3
	return 4 * x / d, 6 * y / d
}

func uvToXY(u, v float64) (float64, float64) {
	d := 2*u - 8*v + 4
	return 3 * u / d, 2 * v / d
}

func planckXY(t float64) (float64, float64) {
	t = clamp(t, 1667, 25000)
	t2, t3 := t*t, t*t*t
	var x float64
	if t <= 4000 {
		x = -0.2661239e9/t3 - 0.2343589e6/t2 + 0.8776956e3/t + 0.179910
	} else {
		x = -3.0258469e9/t3 + 2.1070379e6/t2 + 0.2226347e3/t + 0.240390
	}
	x2, x3 := x*x, x*x*x
	var y float64
	switch {
	case t <= 2222:
		y = -1.1063814*x3 - 1.34811020*x2 + 2.18555832*x - 0.20219683
	case t <= 4000:
		y = -0.9549476*x3 - 1.37418593*x2 + 2.09137015*x - 0.16748867
	default:
		y = 3.0817580*x3 - 5.87338670*x2 + 3.75112997*x - 0.37001483
	}
	return x, y
}

func locusNormal(t float64) (float64, float64) {
	a := math.Max(1667, t-10)
	b := math.Min(25000, t+10)
	u1, v1 := xyToUV(planckXY(a))
	u2, v2 := xyToUV(planckXY(b))
	du, dv := u2-u1, v2-v1
	n := math.Hypot(du, dv)
	nu, nv := -dv/n, du/n
	if nv < 0 {
		nu, nv = -nu, -nv
	}
	return nu, nv
}

func TempTintToXY(temp, tint float64) (float64, float64) {
	t := clamp(temp, 1667, 25000)
	u, v := xyToUV(planckXY(t))
	nu, nv := locusNormal(t)
	duv := tint / tintScale
	return uvToXY(u+duv*nu, v+duv*nv)
}

func XYToTempTint(x, y float64) (float64, float64) {
	u, v := xyToUV(x, y)
	dist := func(lt float64) float64 {
		pu, pv := xyToUV(planckXY(math.Exp(lt)))
		return (pu-u)*(pu-u) + (pv-v)*(pv-v)
	}
	phi := (math.Sqrt(5) - 1) / 2
	a, b := math.Log(1667), math.Log(25000)
	c := b - phi*(b-a)
	d := a + phi*(b-a)
	fc, fd := dist(c), dist(d)
	for i := 0; i < 100; i++ {
		if fc < fd {
			b, d, fd = d, c, fc
			c = b - phi*(b-a)
			fc = dist(c)
		} else {
			a, c, fc = c, d, fd
			d = a + phi*(b-a)
			fd = dist(d)
		}
	}
	t := math.Exp((a + b) / 2)
	pu, pv := xyToUV(planckXY(t))
	nu, nv := locusNormal(t)
	duv := (u-pu)*nu + (v-pv)*nv
	return t, duv * tintScale
}

func mulsForWhite(camToXYZ Mat3, white [3]float64) ([3]float64, error) {
	inv, ok := camToXYZ.Inverse()
	if !ok {
		return [3]float64{}, errors.New("camera matrix is singular")
	}
	raw := inv.Apply(white)
	if raw[0] <= 0 || raw[1] <= 0 || raw[2] <= 0 {
		return [3]float64{}, errors.New("white point is outside the camera response")
	}
	return [3]float64{raw[1] / raw[0], 1, raw[1] / raw[2]}, nil
}

func whiteOfMuls(camToXYZ Mat3, mul [3]float64) [3]float64 {
	return camToXYZ.Apply([3]float64{1 / mul[0], 1 / mul[1], 1 / mul[2]})
}

func solveCameraMatrix(cam, xyz *Image, mul [3]float64) (Mat3, error) {
	if cam.W != xyz.W || cam.H != xyz.H {
		return Mat3{}, errors.New("calibration renders differ in size")
	}
	var ata Mat3
	var aty [3][3]float64
	n := cam.W * cam.H
	step := n / 200000
	if step < 1 {
		step = 1
	}
	count := 0
	for i := 0; i < n; i += step {
		c := cam.Pix[i*3 : i*3+3]
		if c[0] <= 0.01 || c[1] <= 0.01 || c[2] <= 0.01 || c[0] >= 0.85 || c[1] >= 0.85 || c[2] >= 0.85 {
			continue
		}
		r := [3]float64{float64(c[0]) / mul[0], float64(c[1]) / mul[1], float64(c[2]) / mul[2]}
		y := xyz.Pix[i*3 : i*3+3]
		for a := 0; a < 3; a++ {
			for b := 0; b < 3; b++ {
				ata[a][b] += r[a] * r[b]
			}
			for o := 0; o < 3; o++ {
				aty[o][a] += float64(y[o]) * r[a]
			}
		}
		count++
	}
	if count < 1000 {
		return Mat3{}, errors.New("not enough unclipped pixels to calibrate camera")
	}
	inv, ok := ata.Inverse()
	if !ok {
		return Mat3{}, errors.New("calibration system is singular")
	}
	var m Mat3
	for o := 0; o < 3; o++ {
		for j := 0; j < 3; j++ {
			for a := 0; a < 3; a++ {
				m[o][j] += inv[j][a] * aty[o][a]
			}
		}
	}
	return m, nil
}
