package models

import (
	"database/sql"
	"encoding/json"
	"time"
)

type Folder struct {
	ID             int
	ParentID       sql.NullInt64
	Name           string
	Path           string
	CoverPhotoID   sql.NullInt64
	CreatedAt      time.Time
	UpdatedAt      time.Time
	PhotoCount     int
	SubfolderCount int
	CoverURL       string
	PreviewURLs    []string
	Depth          int
	HasChildren    bool
	TotalSize      int64
	LatestPhoto    sql.NullTime
}

type Photo struct {
	ID          int
	FolderID    sql.NullInt64
	Filename    string
	Path        string
	URLPath     string
	Title       sql.NullString
	Description sql.NullString
	Note        sql.NullString
	Width       int
	Height      int
	SizeBytes   int64
	Blurhash    sql.NullString
	ExifData    json.RawMessage
	Hidden      bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
	TakenAt     sql.NullTime
}

type ExifInfo struct {
	CameraMake       string `json:"camera_make,omitempty"`
	CameraModel      string `json:"camera_model,omitempty"`
	LensModel        string `json:"lens_model,omitempty"`
	FocalLength      string `json:"focal_length,omitempty"`
	FocalLength35mm  string `json:"focal_length_35mm,omitempty"`
	Aperture         string `json:"aperture,omitempty"`
	ShutterSpeed     string `json:"shutter_speed,omitempty"`
	ISO              int    `json:"iso,omitempty"`
	ExposureComp     string `json:"exposure_comp,omitempty"`
	Flash            string `json:"flash,omitempty"`
	WhiteBalance     string `json:"white_balance,omitempty"`
	MeteringMode     string `json:"metering_mode,omitempty"`
	ExposureMode     string `json:"exposure_mode,omitempty"`
	ColorSpace       string `json:"color_space,omitempty"`
	Orientation      int    `json:"orientation,omitempty"`
	Software         string `json:"software,omitempty"`
	DateTimeOriginal string `json:"datetime_original,omitempty"`
	Artist           string `json:"artist,omitempty"`
	Copyright        string `json:"copyright,omitempty"`
	ImageDescription string `json:"image_description,omitempty"`
}
