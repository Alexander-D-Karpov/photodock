# CLAUDE.md

Guidance for working in this repo. PhotoDock is a self-hosted photo gallery:
Go stdlib HTTP + pgx/PostgreSQL, `html/template`, and vanilla JS/CSS. All web
assets are compiled into the binary with `go:embed` — there is **no frontend
build step**.

## Commands

```sh
make ci        # the gate: fmt-check → vet → lint → test (-race) → build. Run before pushing.
make test      # go test -race ./...
make lint      # golangci-lint run   (golangci-lint v2; config in .golangci.yml)
make fmt       # gofmt -s -w .
make build     # -> bin/photodock
make dev       # go run ./cmd/photodock   (needs env vars + Postgres)
make deploy    # build linux/amd64 `main`, scp to server, install + restart photodock.service
```

Running the app needs `DATABASE_URL`, `MEDIA_ROOT`, `ADMIN_PASS` (see README table);
migrations run automatically on startup. The existing tests do **not** need a
database (they avoid DB-touching routes), so `make test` runs anywhere.

## Architecture

```
cmd/photodock/main.go        entrypoint: config → db+migrate → services → handlers → http.Server
cmd/photodock/web/           go:embed'd templates/ + static/ (css, js, icon.webp)
internal/config/             env-var config (Load)
internal/database/           pgx pool + migrations (auto-run)
internal/handlers/           all HTTP handlers, routing (RegisterRoutes), template loading
internal/services/           exif (exiftool + goexif fallback), thumbnail (imaging), scanner
internal/models/             Folder, Photo, ExifInfo
deploy/                      RUNBOOK.md (server-only ops) + logrotate-nginx
```

Public routes: `/`, `/folder/{id}`, `/p/{path...}`, `/photo/{id}`, `/thumb/{size}/{id}`,
`/original/{id}`, `/placeholder/{id}`, `/ogcard/folder/{id}`, `/favicon.ico`.
Admin routes under `/admin/*` use basic auth (`ADMIN_USER`/`ADMIN_PASS`).
`/api/v1/token` and `POST /api/v1/staging` use Bearer API tokens (`tokens.go`, managed at `/admin/tokens`).
Notable admin ops: `POST /admin/reprocess` (rebuild exif/dims/blurhash),
`POST /admin/regenerate-urls`.

## Conventions & gotchas

- **Templates parse at runtime**, not at `go build`. `handlers.LoadTemplates(webFS)`
  walks `web/templates/**/*.html`. A template syntax error only surfaces when the
  server starts (or in `cmd/photodock/main_test.go` `TestEmbeddedTemplatesParse`).
  **After editing any template, run that test.** Each file is `{{define "public/x.html"}}`
  matching its path; handlers render by that name.
- **Static asset URLs use the `asset` template func** for cache-busting: write
  `href="{{asset "/static/css/public.css"}}"`, never a bare `/static/...` path.
  `asset` appends `?v=<content-hash>` (see `staticAssetVersions`), so nginx can
  cache aggressively and busts automatically when a file's bytes change.
- **Template helpers live in one place**: `handlers.templateFuncMap`. Add new
  helpers there so both `LoadTemplates` and its tests see them.
- **golangci-lint is v2** (`.golangci.yml` has `version: "2"`, a `formatters`
  section, and `linters.default: standard` plus a few extras). v1-style config
  won't parse. CI uses `golangci/golangci-lint-action@v8` with `version: v2.11`.
- **EXIF**: `ExifService` prefers the `exiftool` binary (`-json -a -G1 -all
  -Orientation#`, PrintConv on so values are human strings) and falls back to
  `goexif`. `exiftool` must be on PATH at startup (systemd unit sets PATH to
  include Arch's `vendor_perl`).
- **Portrait dimensions**: cameras store portrait shots as landscape pixels +
  EXIF orientation 5–8. `services.orientedDimensions(w, h, orientation)` swaps
  them; it is applied in `scanner.go` right after `GetImageDimensions` in both
  `processPhoto` and `ReprocessAllMetadata`. Keep the two call sites in sync.
- **Gallery layout** (`web/static/js/gallery.js`): vanilla JS justified-rows.
  `layoutJustified()` wraps `.photo-item` anchors into `.justified-row` flex rows
  using each photo's inline `aspect-ratio`; a `ResizeObserver` on `#gallery`
  relays out on **width** changes only (reacting to height would loop). Order is
  the DOM order / `data-idx`; `.load-more-trigger` stays last. `window.layoutJustified`
  is the integration point for `index.js` sort.
- **Private folders** (`folders.private`, inherited by subfolders via path prefix): every public
  route and `/api/*` must filter with `photoVisible`/`folderVisible` (`handlers/private.go`).
  Admin sees them via basic auth or the `photodock_admin` cookie set by `adminAuth`; private
  responses are sent `Cache-Control: private, no-store` so nginx never caches them.
- **Deploy is server-coupled**: production runs on `akarpov.ru` (Arch, systemd,
  nginx, Postgres). Server/DNS/logrotate/Cloudflare steps that can't be done from
  a checkout are documented in `deploy/RUNBOOK.md` — don't attempt to SSH/deploy
  or change DNS from a dev session; hand those to the operator.

## Verifying UI/EXIF changes

Pure-Go tests + `cmd/photodock/main_test.go` cover routing, template parsing, and
asset embedding without a DB. Full-page rendering and the browser gallery layout
need a running server with Postgres and real photos — verify those against a live
instance (see `deploy/RUNBOOK.md` for the reprocess + SQL checks).
