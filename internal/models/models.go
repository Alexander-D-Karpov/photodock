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
	// Camera
	CameraMake      string `json:"camera_make,omitempty"`
	CameraModel     string `json:"camera_model,omitempty"`
	LensModel       string `json:"lens_model,omitempty"`
	LensInfo        string `json:"lens_info,omitempty"`
	SerialNumber    string `json:"serial_number,omitempty"`
	FirmwareVersion string `json:"firmware_version,omitempty"`

	// Lens & Focus
	FocalLength      string `json:"focal_length,omitempty"`
	FocalLength35mm  string `json:"focal_length_35mm,omitempty"`
	MaxFocalLength   string `json:"max_focal_length,omitempty"`
	MinFocalLength   string `json:"min_focal_length,omitempty"`
	MaxApertureValue string `json:"max_aperture_value,omitempty"`
	FocusMode        string `json:"focus_mode,omitempty"`
	FocusDistance    string `json:"focus_distance,omitempty"`
	DepthOfField     string `json:"depth_of_field,omitempty"`
	HyperfocalDist   string `json:"hyperfocal_distance,omitempty"`

	// Exposure
	Aperture        string `json:"aperture,omitempty"`
	ShutterSpeed    string `json:"shutter_speed,omitempty"`
	ISO             int    `json:"iso,omitempty"`
	ExposureComp    string `json:"exposure_comp,omitempty"`
	ExposureMode    string `json:"exposure_mode,omitempty"`
	ExposureProgram string `json:"exposure_program,omitempty"`
	MeteringMode    string `json:"metering_mode,omitempty"`
	LightValue      string `json:"light_value,omitempty"`
	BrightnessValue string `json:"brightness_value,omitempty"`

	// Flash
	Flash             string `json:"flash,omitempty"`
	FlashMode         string `json:"flash_mode,omitempty"`
	FlashExposureComp string `json:"flash_exposure_comp,omitempty"`

	// White Balance & Color
	WhiteBalance     string `json:"white_balance,omitempty"`
	ColorSpace       string `json:"color_space,omitempty"`
	ColorTemperature int    `json:"color_temperature,omitempty"`
	Saturation       string `json:"saturation,omitempty"`
	Contrast         string `json:"contrast,omitempty"`
	Sharpness        string `json:"sharpness,omitempty"`

	// Scene & Mode
	SceneCaptureType   string `json:"scene_capture_type,omitempty"`
	ShootingMode       string `json:"shooting_mode,omitempty"`
	DriveMode          string `json:"drive_mode,omitempty"`
	MacroMode          string `json:"macro_mode,omitempty"`
	SelfTimer          string `json:"self_timer,omitempty"`
	DigitalZoom        string `json:"digital_zoom,omitempty"`
	ImageStabilization string `json:"image_stabilization,omitempty"`

	// Image
	Orientation   int    `json:"orientation,omitempty"`
	Quality       string `json:"quality,omitempty"`
	ImageWidth    int    `json:"image_width,omitempty"`
	ImageHeight   int    `json:"image_height,omitempty"`
	BitsPerSample int    `json:"bits_per_sample,omitempty"`
	Compression   string `json:"compression,omitempty"`

	// Dates
	DateTimeOriginal string `json:"datetime_original,omitempty"`
	CreateDate       string `json:"create_date,omitempty"`
	ModifyDate       string `json:"modify_date,omitempty"`

	// Author
	Artist           string `json:"artist,omitempty"`
	Copyright        string `json:"copyright,omitempty"`
	OwnerName        string `json:"owner_name,omitempty"`
	Software         string `json:"software,omitempty"`
	ImageDescription string `json:"image_description,omitempty"`

	// Technical
	FileSource       string `json:"file_source,omitempty"`
	SceneType        string `json:"scene_type,omitempty"`
	SensingMethod    string `json:"sensing_method,omitempty"`
	CustomRendered   string `json:"custom_rendered,omitempty"`
	GainControl      string `json:"gain_control,omitempty"`
	SubjectDistance  string `json:"subject_distance,omitempty"`
	SubjectDistRange string `json:"subject_distance_range,omitempty"`

	// Camera-specific
	CameraTemperature string `json:"camera_temperature,omitempty"`
	FileNumber        string `json:"file_number,omitempty"`
	ImageUniqueID     string `json:"image_unique_id,omitempty"`
}
