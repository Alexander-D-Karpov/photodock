package services

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/photodock/internal/models"
	"github.com/rwcarlsen/goexif/exif"
)

type ExifService struct {
	hasExiftool bool
}

func NewExifService() *ExifService {
	_, err := exec.LookPath("exiftool")
	return &ExifService{
		hasExiftool: err == nil,
	}
}

func (s *ExifService) Extract(path string) (*models.ExifInfo, time.Time, error) {
	if s.hasExiftool {
		return s.extractWithExiftool(path)
	}
	return s.extractWithGoexif(path)
}

func (s *ExifService) extractWithExiftool(path string) (*models.ExifInfo, time.Time, error) {
	cmd := exec.Command("exiftool", "-json", "-a", "-G1", "-n", path)
	output, err := cmd.Output()
	if err != nil {
		return s.extractWithGoexif(path)
	}

	var results []map[string]interface{}
	if err := json.Unmarshal(output, &results); err != nil || len(results) == 0 {
		return s.extractWithGoexif(path)
	}

	data := results[0]
	info := &models.ExifInfo{}
	var takenAt time.Time

	info.CameraMake = getString(data, "IFD0:Make")
	info.CameraModel = getString(data, "IFD0:Model")
	if info.CameraModel == "" {
		info.CameraModel = getString(data, "IFD0:CameraModelName")
	}

	info.LensModel = getString(data, "ExifIFD:LensModel")
	if info.LensModel == "" {
		info.LensModel = getString(data, "Composite:Lens")
	}
	info.LensInfo = getString(data, "ExifIFD:LensInfo")

	if fl := getFloat(data, "ExifIFD:FocalLength"); fl > 0 {
		info.FocalLength = fmt.Sprintf("%.1f mm", fl)
	}
	if fl35 := getFloat(data, "ExifIFD:FocalLengthIn35mmFormat"); fl35 > 0 {
		info.FocalLength35mm = fmt.Sprintf("%.0f mm", fl35)
	} else if fl35 := getFloat(data, "Composite:FocalLength35efl"); fl35 > 0 {
		info.FocalLength35mm = fmt.Sprintf("%.1f mm", fl35)
	}

	if minFL := getFloat(data, "Canon:MinFocalLength"); minFL > 0 {
		maxFL := getFloat(data, "Canon:MaxFocalLength")
		info.LensInfo = fmt.Sprintf("%.0f - %.0f mm", minFL, maxFL)
	}

	if fn := getFloat(data, "ExifIFD:FNumber"); fn > 0 {
		info.Aperture = fmt.Sprintf("f/%.1f", fn)
	}
	if maxAp := getFloat(data, "ExifIFD:MaxApertureValue"); maxAp > 0 {
		fnum := math.Pow(2, maxAp/2)
		info.MaxApertureValue = fmt.Sprintf("f/%.1f", fnum)
	}

	if et := getFloat(data, "ExifIFD:ExposureTime"); et > 0 {
		if et >= 1 {
			info.ShutterSpeed = fmt.Sprintf("%.1f s", et)
		} else {
			info.ShutterSpeed = fmt.Sprintf("1/%.0f s", 1/et)
		}
	}

	if iso := getInt(data, "ExifIFD:ISO"); iso > 0 {
		info.ISO = iso
	}

	if ec := getFloat(data, "ExifIFD:ExposureCompensation"); ec != 0 {
		if ec >= 0 {
			info.ExposureComp = fmt.Sprintf("+%.1f EV", ec)
		} else {
			info.ExposureComp = fmt.Sprintf("%.1f EV", ec)
		}
	} else {
		info.ExposureComp = "0 EV"
	}

	info.ExposureMode = decodeExposureModeValue(getInt(data, "ExifIFD:ExposureMode"))
	info.ExposureProgram = decodeExposureProgram(getInt(data, "ExifIFD:ExposureProgram"))
	info.MeteringMode = decodeMeteringMode(getInt(data, "ExifIFD:MeteringMode"))

	if lv := getFloat(data, "Composite:LightValue"); lv != 0 {
		info.LightValue = fmt.Sprintf("%.1f", lv)
	}

	flash := getInt(data, "ExifIFD:Flash")
	info.Flash = decodeFlash(flash)
	info.FlashMode = getString(data, "Canon:CanonFlashMode")
	if fec := getFloat(data, "Canon:FlashExposureComp"); fec != 0 {
		info.FlashExposureComp = fmt.Sprintf("%.1f EV", fec)
	}

	wb := getInt(data, "ExifIFD:WhiteBalance")
	if wb == 0 {
		info.WhiteBalance = "Auto"
	} else {
		info.WhiteBalance = "Manual"
	}
	if ct := getInt(data, "Canon:ColorTemperature"); ct > 0 {
		info.ColorTemperature = ct
	}

	info.ColorSpace = decodeColorSpace(getInt(data, "ExifIFD:ColorSpace"))

	info.FocusMode = getString(data, "Canon:FocusMode")
	if info.FocusMode == "" {
		info.FocusMode = getString(data, "ExifIFD:FocusMode")
	}

	if fdUpper := getFloat(data, "Canon:FocusDistanceUpper"); fdUpper > 0 {
		fdLower := getFloat(data, "Canon:FocusDistanceLower")
		if fdLower > 0 && fdLower != fdUpper {
			info.FocusDistance = fmt.Sprintf("%.2f - %.2f m", fdLower, fdUpper)
		} else {
			info.FocusDistance = fmt.Sprintf("%.2f m", fdUpper)
		}
	}

	if sd := getFloat(data, "ExifIFD:SubjectDistance"); sd > 0 {
		if sd < 1 {
			info.SubjectDistance = fmt.Sprintf("%.0f cm", sd*100)
		} else {
			info.SubjectDistance = fmt.Sprintf("%.2f m", sd)
		}
	}

	info.DepthOfField = getString(data, "Composite:DOF")
	info.HyperfocalDist = getString(data, "Composite:HyperfocalDistance")

	info.Contrast = decodeCanonLevel(getString(data, "Canon:Contrast"))
	info.Saturation = decodeCanonLevel(getString(data, "Canon:Saturation"))
	info.Sharpness = getString(data, "Canon:Sharpness")
	if info.Sharpness == "0" {
		info.Sharpness = "Normal"
	}

	info.SceneCaptureType = decodeSceneCaptureType(getInt(data, "ExifIFD:SceneCaptureType"))
	info.ShootingMode = getString(data, "Composite:ShootingMode")
	if info.ShootingMode == "" {
		info.ShootingMode = getString(data, "Canon:EasyMode")
	}
	info.DriveMode = getString(data, "Composite:DriveMode")
	if info.DriveMode == "" {
		info.DriveMode = getString(data, "Canon:ContinuousDrive")
	}
	info.MacroMode = getString(data, "Canon:MacroMode")
	info.SelfTimer = getString(data, "Canon:SelfTimer")
	info.ImageStabilization = getString(data, "Canon:ImageStabilization")

	dzr := getFloat(data, "ExifIFD:DigitalZoomRatio")
	if dzr <= 1 {
		info.DigitalZoom = "None"
	} else {
		info.DigitalZoom = fmt.Sprintf("%.1fx", dzr)
	}

	info.Quality = getString(data, "Canon:Quality")
	info.Orientation = getInt(data, "IFD0:Orientation")

	info.SensingMethod = decodeSensingMethod(getInt(data, "ExifIFD:SensingMethod"))
	info.FileSource = getString(data, "ExifIFD:FileSource")
	if info.FileSource == "3" {
		info.FileSource = "Digital Camera"
	}
	cr := getInt(data, "ExifIFD:CustomRendered")
	if cr == 0 {
		info.CustomRendered = "Normal"
	} else if cr == 1 {
		info.CustomRendered = "Custom"
	}

	info.FirmwareVersion = getString(data, "Canon:FirmwareVersion")
	if info.FirmwareVersion == "" {
		info.FirmwareVersion = getString(data, "Canon:CanonFirmwareVersion")
	}
	info.SerialNumber = getString(data, "Canon:SerialNumber")
	if info.SerialNumber == "" {
		info.SerialNumber = getString(data, "ExifIFD:SerialNumber")
	}
	info.CameraTemperature = getString(data, "Canon:CameraTemperature")
	info.FileNumber = getString(data, "Canon:FileNumber")
	info.OwnerName = getString(data, "Canon:OwnerName")
	info.ImageUniqueID = getString(data, "Canon:ImageUniqueID")
	if info.ImageUniqueID == "" {
		info.ImageUniqueID = getString(data, "ExifIFD:ImageUniqueID")
	}

	if dto := getString(data, "ExifIFD:DateTimeOriginal"); dto != "" {
		if t, err := time.Parse("2006:01:02 15:04:05", dto); err == nil {
			takenAt = t
			info.DateTimeOriginal = t.Format("2006-01-02 15:04:05")
		}
	}
	info.CreateDate = getString(data, "ExifIFD:CreateDate")
	info.ModifyDate = getString(data, "IFD0:ModifyDate")

	info.Artist = getString(data, "IFD0:Artist")
	info.Copyright = getString(data, "IFD0:Copyright")
	info.Software = getString(data, "IFD0:Software")
	info.ImageDescription = getString(data, "IFD0:ImageDescription")

	info.ImageWidth = getInt(data, "File:ImageWidth")
	info.ImageHeight = getInt(data, "File:ImageHeight")

	return info, takenAt, nil
}

func getString(data map[string]interface{}, key string) string {
	if v, ok := data[key]; ok {
		switch val := v.(type) {
		case string:
			return strings.TrimSpace(val)
		case float64:
			if val == float64(int(val)) {
				return strconv.Itoa(int(val))
			}
			return fmt.Sprintf("%.2f", val)
		case int:
			return strconv.Itoa(val)
		}
	}
	return ""
}

func getFloat(data map[string]interface{}, key string) float64 {
	if v, ok := data[key]; ok {
		switch val := v.(type) {
		case float64:
			return val
		case int:
			return float64(val)
		case string:
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				return f
			}
		}
	}
	return 0
}

func getInt(data map[string]interface{}, key string) int {
	if v, ok := data[key]; ok {
		switch val := v.(type) {
		case float64:
			return int(val)
		case int:
			return val
		case string:
			if i, err := strconv.Atoi(val); err == nil {
				return i
			}
		}
	}
	return 0
}

func decodeCanonLevel(s string) string {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "0", "normal":
		return "Normal"
	case "1", "low":
		return "Low"
	case "2", "high":
		return "High"
	default:
		if s != "" {
			return s
		}
		return ""
	}
}

func decodeExposureModeValue(val int) string {
	modes := map[int]string{0: "Auto", 1: "Manual", 2: "Auto bracket"}
	if m, ok := modes[val]; ok {
		return m
	}
	return ""
}

func decodeColorSpace(val int) string {
	switch val {
	case 1:
		return "sRGB"
	case 2:
		return "Adobe RGB"
	case 65535:
		return "Uncalibrated"
	}
	return ""
}

func (s *ExifService) extractWithGoexif(path string) (*models.ExifInfo, time.Time, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer func() { _ = f.Close() }()

	x, err := exif.Decode(f)
	if err != nil {
		return &models.ExifInfo{}, time.Time{}, nil
	}

	info := &models.ExifInfo{}
	var takenAt time.Time

	info.CameraMake = s.getStringTag(x, exif.Make)
	info.CameraModel = s.getStringTag(x, exif.Model)
	info.LensModel = s.getStringTag(x, exif.LensModel)
	info.Software = s.getStringTag(x, exif.Software)
	info.Artist = s.getStringTag(x, exif.Artist)
	info.Copyright = s.getStringTag(x, exif.Copyright)
	info.ImageDescription = s.getStringTag(x, exif.ImageDescription)

	if tag, err := x.Get(exif.FocalLength); err == nil {
		if num, denom, err := tag.Rat2(0); err == nil && denom != 0 {
			fl := float64(num) / float64(denom)
			info.FocalLength = fmt.Sprintf("%.1f mm", fl)
		}
	}

	if tag, err := x.Get(exif.FocalLengthIn35mmFilm); err == nil {
		if val, err := tag.Int(0); err == nil {
			info.FocalLength35mm = fmt.Sprintf("%d mm", val)
		}
	}

	if tag, err := x.Get(exif.FNumber); err == nil {
		if num, denom, err := tag.Rat2(0); err == nil && denom != 0 {
			info.Aperture = fmt.Sprintf("f/%.1f", float64(num)/float64(denom))
		}
	}

	if tag, err := x.Get(exif.MaxApertureValue); err == nil {
		if num, denom, err := tag.Rat2(0); err == nil && denom != 0 {
			apex := float64(num) / float64(denom)
			fnum := math.Pow(2, apex/2)
			info.MaxApertureValue = fmt.Sprintf("f/%.1f", fnum)
		}
	}

	if tag, err := x.Get(exif.ExposureTime); err == nil {
		if num, denom, err := tag.Rat2(0); err == nil && denom != 0 {
			et := float64(num) / float64(denom)
			if et >= 1 {
				info.ShutterSpeed = fmt.Sprintf("%.1f s", et)
			} else {
				info.ShutterSpeed = fmt.Sprintf("1/%.0f s", 1/et)
			}
		}
	}

	if tag, err := x.Get(exif.ISOSpeedRatings); err == nil {
		if val, err := tag.Int(0); err == nil {
			info.ISO = val
		}
	}

	if tag, err := x.Get(exif.ExposureBiasValue); err == nil {
		if num, denom, err := tag.Rat2(0); err == nil && denom != 0 {
			val := float64(num) / float64(denom)
			if val >= 0 {
				info.ExposureComp = fmt.Sprintf("+%.1f EV", val)
			} else {
				info.ExposureComp = fmt.Sprintf("%.1f EV", val)
			}
		}
	}

	if tag, err := x.Get(exif.Flash); err == nil {
		if val, err := tag.Int(0); err == nil {
			info.Flash = decodeFlash(val)
		}
	}

	if tag, err := x.Get(exif.WhiteBalance); err == nil {
		if val, err := tag.Int(0); err == nil {
			if val == 0 {
				info.WhiteBalance = "Auto"
			} else {
				info.WhiteBalance = "Manual"
			}
		}
	}

	if tag, err := x.Get(exif.MeteringMode); err == nil {
		if val, err := tag.Int(0); err == nil {
			info.MeteringMode = decodeMeteringMode(val)
		}
	}

	if tag, err := x.Get(exif.ExposureMode); err == nil {
		if val, err := tag.Int(0); err == nil {
			info.ExposureMode = decodeExposureMode(val)
		}
	}

	if tag, err := x.Get(exif.ExposureProgram); err == nil {
		if val, err := tag.Int(0); err == nil {
			info.ExposureProgram = decodeExposureProgram(val)
		}
	}

	if tag, err := x.Get(exif.ColorSpace); err == nil {
		if val, err := tag.Int(0); err == nil {
			info.ColorSpace = decodeColorSpace(val)
		}
	}

	if tag, err := x.Get(exif.Orientation); err == nil {
		if val, err := tag.Int(0); err == nil {
			info.Orientation = val
		}
	}

	if tag, err := x.Get(exif.SceneCaptureType); err == nil {
		if val, err := tag.Int(0); err == nil {
			info.SceneCaptureType = decodeSceneCaptureType(val)
		}
	}

	if tag, err := x.Get(exif.DigitalZoomRatio); err == nil {
		if num, denom, err := tag.Rat2(0); err == nil && denom != 0 {
			ratio := float64(num) / float64(denom)
			if ratio <= 1 {
				info.DigitalZoom = "None"
			} else {
				info.DigitalZoom = fmt.Sprintf("%.1fx", ratio)
			}
		}
	}

	if tag, err := x.Get(exif.SensingMethod); err == nil {
		if val, err := tag.Int(0); err == nil {
			info.SensingMethod = decodeSensingMethod(val)
		}
	}

	if tag, err := x.Get(exif.CustomRendered); err == nil {
		if val, err := tag.Int(0); err == nil {
			if val == 0 {
				info.CustomRendered = "Normal"
			} else {
				info.CustomRendered = "Custom"
			}
		}
	}

	if tag, err := x.Get(exif.Contrast); err == nil {
		if val, err := tag.Int(0); err == nil {
			info.Contrast = decodeLevelTag(val)
		}
	}

	if tag, err := x.Get(exif.Saturation); err == nil {
		if val, err := tag.Int(0); err == nil {
			info.Saturation = decodeLevelTag(val)
		}
	}

	if tag, err := x.Get(exif.Sharpness); err == nil {
		if val, err := tag.Int(0); err == nil {
			info.Sharpness = decodeLevelTag(val)
		}
	}

	if tag, err := x.Get(exif.SubjectDistance); err == nil {
		if num, denom, err := tag.Rat2(0); err == nil && denom != 0 {
			dist := float64(num) / float64(denom)
			if dist < 1 {
				info.SubjectDistance = fmt.Sprintf("%.0f cm", dist*100)
			} else {
				info.SubjectDistance = fmt.Sprintf("%.2f m", dist)
			}
		}
	}

	if tag, err := x.Get(exif.SubjectDistanceRange); err == nil {
		if val, err := tag.Int(0); err == nil {
			info.SubjectDistRange = decodeSubjectDistRange(val)
		}
	}

	if tag, err := x.Get(exif.GainControl); err == nil {
		if val, err := tag.Int(0); err == nil {
			info.GainControl = decodeGainControl(val)
		}
	}

	if tag, err := x.Get(exif.PixelXDimension); err == nil {
		if val, err := tag.Int(0); err == nil {
			info.ImageWidth = val
		}
	}

	if tag, err := x.Get(exif.PixelYDimension); err == nil {
		if val, err := tag.Int(0); err == nil {
			info.ImageHeight = val
		}
	}

	if tag, err := x.Get(exif.ImageUniqueID); err == nil {
		if v, err := tag.StringVal(); err == nil {
			info.ImageUniqueID = cleanString(v)
		}
	}

	if tm, err := x.DateTime(); err == nil {
		takenAt = tm
		info.DateTimeOriginal = tm.Format("2006-01-02 15:04:05")
	}

	return info, takenAt, nil
}

func (s *ExifService) getStringTag(x *exif.Exif, field exif.FieldName) string {
	if tag, err := x.Get(field); err == nil {
		if v, err := tag.StringVal(); err == nil {
			return cleanString(v)
		}
	}
	return ""
}

func (s *ExifService) StripGPS(path string) error {
	return stripGPSFromJPEG(path)
}

func stripGPSFromJPEG(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil
	}

	modified := false
	result := []byte{0xFF, 0xD8}
	pos := 2

	for pos < len(data)-1 {
		if data[pos] != 0xFF {
			result = append(result, data[pos:]...)
			break
		}

		marker := data[pos+1]
		if marker == 0xD9 {
			result = append(result, data[pos:]...)
			break
		}
		if marker == 0xDA {
			result = append(result, data[pos:]...)
			break
		}

		if pos+4 > len(data) {
			break
		}
		segLen := int(binary.BigEndian.Uint16(data[pos+2:pos+4])) + 2

		if marker == 0xE1 && pos+10 < len(data) {
			if string(data[pos+4:pos+10]) == "Exif\x00\x00" {
				cleaned, changed := removeGPSFromExif(data[pos : pos+segLen])
				if changed {
					modified = true
				}
				result = append(result, cleaned...)
				pos += segLen
				continue
			}
		}

		result = append(result, data[pos:pos+segLen]...)
		pos += segLen
	}

	if modified {
		return os.WriteFile(path, result, 0644)
	}
	return nil
}

func removeGPSFromExif(segment []byte) ([]byte, bool) {
	if len(segment) < 12 {
		return segment, false
	}

	exifData := segment[10:]
	if len(exifData) < 8 {
		return segment, false
	}

	var bo binary.ByteOrder
	if string(exifData[0:2]) == "II" {
		bo = binary.LittleEndian
	} else if string(exifData[0:2]) == "MM" {
		bo = binary.BigEndian
	} else {
		return segment, false
	}

	ifdOffset := bo.Uint32(exifData[4:8])
	if int(ifdOffset) >= len(exifData) {
		return segment, false
	}

	result := make([]byte, len(segment))
	copy(result, segment)
	exifResult := result[10:]
	modified := false

	offset := int(ifdOffset)
	if offset+2 > len(exifResult) {
		return segment, false
	}

	numEntries := int(bo.Uint16(exifResult[offset : offset+2]))
	offset += 2

	for i := 0; i < numEntries; i++ {
		entryOffset := offset + i*12
		if entryOffset+12 > len(exifResult) {
			break
		}

		tag := bo.Uint16(exifResult[entryOffset : entryOffset+2])
		if tag == 0x8825 {
			for j := entryOffset; j < entryOffset+12; j++ {
				exifResult[j] = 0
			}
			modified = true
		}
	}

	return result, modified
}

func cleanString(s string) string {
	return strings.TrimSpace(strings.Trim(s, "\x00"))
}

func decodeFlash(val int) string {
	fired := val&1 == 1
	mode := (val >> 3) & 3

	var parts []string

	if fired {
		parts = append(parts, "Fired")
	} else {
		parts = append(parts, "Did not fire")
	}

	switch mode {
	case 1:
		parts = append(parts, "compulsory")
	case 2:
		parts = append(parts, "suppressed")
	case 3:
		parts = append(parts, "auto")
	}

	return strings.Join(parts, ", ")
}

func decodeMeteringMode(val int) string {
	modes := map[int]string{
		0: "Unknown", 1: "Average", 2: "Center-weighted average", 3: "Spot",
		4: "Multi-spot", 5: "Multi-segment", 6: "Partial", 255: "Other",
	}
	if m, ok := modes[val]; ok {
		return m
	}
	return ""
}

func decodeExposureMode(val int) string {
	modes := map[int]string{0: "Auto", 1: "Manual", 2: "Auto bracket"}
	if m, ok := modes[val]; ok {
		return m
	}
	return ""
}

func decodeExposureProgram(val int) string {
	programs := map[int]string{
		0: "Not defined", 1: "Manual", 2: "Program AE", 3: "Aperture priority",
		4: "Shutter priority", 5: "Creative (slow speed)", 6: "Action (high speed)",
		7: "Portrait", 8: "Landscape", 9: "Bulb",
	}
	if p, ok := programs[val]; ok {
		return p
	}
	return ""
}

func decodeSceneCaptureType(val int) string {
	types := map[int]string{
		0: "Standard", 1: "Landscape", 2: "Portrait", 3: "Night scene",
	}
	if t, ok := types[val]; ok {
		return t
	}
	return ""
}

func decodeSubjectDistRange(val int) string {
	ranges := map[int]string{
		0: "Unknown", 1: "Macro", 2: "Close", 3: "Distant",
	}
	if r, ok := ranges[val]; ok {
		return r
	}
	return ""
}

func decodeSensingMethod(val int) string {
	methods := map[int]string{
		1: "Not defined", 2: "One-chip color area", 3: "Two-chip color area",
		4: "Three-chip color area", 5: "Color sequential area",
		7: "Trilinear", 8: "Color sequential linear",
	}
	if m, ok := methods[val]; ok {
		return m
	}
	return ""
}

func decodeGainControl(val int) string {
	controls := map[int]string{
		0: "None", 1: "Low gain up", 2: "High gain up",
		3: "Low gain down", 4: "High gain down",
	}
	if c, ok := controls[val]; ok {
		return c
	}
	return ""
}

func decodeLevelTag(val int) string {
	levels := map[int]string{0: "Normal", 1: "Low", 2: "High"}
	if l, ok := levels[val]; ok {
		return l
	}
	return ""
}
