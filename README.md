# PhotoDock

A self-hosted photo gallery with automatic organization, EXIF extraction, and thumbnail generation.

## Features

- **Automatic scanning** - Recursively scans directories for photos
- **EXIF extraction** - Extracts and displays camera metadata (camera model, lens, aperture, shutter speed, ISO, etc.)
- **GPS stripping** - Automatically removes GPS data from photos for privacy
- **Thumbnail generation** - Creates small and medium thumbnails with lazy loading
- **Blurhash placeholders** - Generates blur placeholders for smooth image loading
- **Folder organization** - Hierarchical folder structure with cover photos
- **Admin panel** - Web-based management interface
- **SEO-friendly URLs** - Clean URL paths for photos and folders
- **Responsive design** - Works on desktop and mobile
- **Dark mode** - Automatic dark/light theme based on system preference
- **Photo viewer** - Full-screen viewer with zoom, pan, and keyboard navigation
- **Chunked uploads** - Support for large file uploads

## Requirements

- Go 1.23+
- PostgreSQL 12+

## Installation

### From source
```bash
git clone https://github.com/Alexander-D-Karpov/photodock.git
cd photodock
go build -o photodock ./cmd/photodock
```

### Configuration

| Variable | Description | Required |
|----------|-------------|----------|
| `DATABASE_URL` | PostgreSQL connection string | Yes |
| `MEDIA_ROOT` | Directory containing your photos | Yes |
| `CACHE_DIR` | Directory for thumbnails and cache (defaults to `MEDIA_ROOT/.photodock_cache`) | No |
| `LISTEN_ADDR` | Address to listen on (default `:8080`) | No |
| `ADMIN_USER` | Admin username (default `admin`) | No |
| `ADMIN_PASS` | Admin password | Yes |

### Database setup
```bash
createdb photodock
```

The application automatically runs migrations on startup.

### Running
```bash
./photodock
```

Or with systemd (see `photodock.service`):
```bash
sudo cp photodock.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now photodock
```

## Usage

### Web interface

- **Gallery**: `/`
- **Admin panel**: `/admin`

### Admin panel

The admin panel allows you to:

- Scan folders for new photos
- Upload photos via drag-and-drop
- Organize photos into folders
- Edit photo metadata (title, description, notes)
- Set folder cover photos
- Hide/show photos
- Delete photos and folders
- Clean orphaned database entries
