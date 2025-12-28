package services

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/photodock/internal/models"
	"github.com/rwcarlsen/goexif/exif"
	"github.com/rwcarlsen/goexif/tiff"
)

type ExifService struct{}

func NewExifService() *ExifService {
	return &ExifService{}
}

func (s *ExifService) Extract(path string) (*models.ExifInfo, time.Time, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer f.Close()

	x, err := exif.Decode(f)
	if err != nil {
		return &models.ExifInfo{}, time.Time{}, nil
	}

	info := &models.ExifInfo{}
	var takenAt time.Time

	if tag, err := x.Get(exif.Make); err == nil {
		if v, err := tag.StringVal(); err == nil {
			info.CameraMake = cleanString(v)
		}
	}
	if tag, err := x.Get(exif.Model); err == nil {
		if v, err := tag.StringVal(); err == nil {
			info.CameraModel = cleanString(v)
		}
	}
	if tag, err := x.Get(exif.LensModel); err == nil {
		if v, err := tag.StringVal(); err == nil {
			info.LensModel = cleanString(v)
		}
	}
	if tag, err := x.Get(exif.FocalLength); err == nil {
		if num, denom, err := tag.Rat2(0); err == nil && denom != 0 {
			info.FocalLength = fmt.Sprintf("%.1f mm", float64(num)/float64(denom))
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
	if tag, err := x.Get(exif.ExposureTime); err == nil {
		if num, denom, err := tag.Rat2(0); err == nil && denom != 0 {
			if num == 1 {
				info.ShutterSpeed = fmt.Sprintf("1/%d s", denom)
			} else {
				info.ShutterSpeed = fmt.Sprintf("%.1f s", float64(num)/float64(denom))
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
	if tag, err := x.Get(exif.ColorSpace); err == nil {
		if val, err := tag.Int(0); err == nil {
			if val == 1 {
				info.ColorSpace = "sRGB"
			} else if val == 65535 {
				info.ColorSpace = "Uncalibrated"
			}
		}
	}
	if tag, err := x.Get(exif.Orientation); err == nil {
		if val, err := tag.Int(0); err == nil {
			info.Orientation = val
		}
	}
	if tag, err := x.Get(exif.Software); err == nil {
		if v, err := tag.StringVal(); err == nil {
			info.Software = cleanString(v)
		}
	}
	if tag, err := x.Get(exif.Artist); err == nil {
		if v, err := tag.StringVal(); err == nil {
			info.Artist = cleanString(v)
		}
	}
	if tag, err := x.Get(exif.Copyright); err == nil {
		if v, err := tag.StringVal(); err == nil {
			info.Copyright = cleanString(v)
		}
	}
	if tag, err := x.Get(exif.ImageDescription); err == nil {
		if v, err := tag.StringVal(); err == nil {
			info.ImageDescription = cleanString(v)
		}
	}
	if tm, err := x.DateTime(); err == nil {
		takenAt = tm
		info.DateTimeOriginal = tm.Format("2006-01-02 15:04:05")
	}

	return info, takenAt, nil
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

	if modified {
		newLen := binary.BigEndian.Uint16(result[2:4])
		binary.BigEndian.PutUint16(result[2:4], newLen)
	}

	return result, modified
}

func cleanString(s string) string {
	return strings.TrimSpace(strings.Trim(s, "\x00"))
}

func decodeFlash(val int) string {
	if val&1 == 0 {
		return "Did not fire"
	}
	return "Fired"
}

func decodeMeteringMode(val int) string {
	modes := map[int]string{
		0: "Unknown", 1: "Average", 2: "Center-weighted", 3: "Spot",
		4: "Multi-spot", 5: "Multi-segment", 6: "Partial",
	}
	if m, ok := modes[val]; ok {
		return m
	}
	return "Unknown"
}

func decodeExposureMode(val int) string {
	modes := map[int]string{0: "Auto", 1: "Manual", 2: "Auto bracket"}
	if m, ok := modes[val]; ok {
		return m
	}
	return "Unknown"
}

type gpsStripper struct{}

func (g gpsStripper) Walk(name exif.FieldName, tag *tiff.Tag) error {
	return nil
}

var _ io.Writer = (*gpsStripper)(nil)

func (g gpsStripper) Write(p []byte) (n int, err error) {
	return len(p), nil
}
