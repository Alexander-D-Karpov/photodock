package database

import "context"

func (db *DB) Migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS folders (
		id SERIAL PRIMARY KEY,
		parent_id INTEGER REFERENCES folders(id) ON DELETE CASCADE,
		name TEXT NOT NULL,
		path TEXT NOT NULL UNIQUE,
		cover_photo_id INTEGER,
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_folders_parent ON folders(parent_id);
	CREATE INDEX IF NOT EXISTS idx_folders_path ON folders(path);

	CREATE TABLE IF NOT EXISTS photos (
		id SERIAL PRIMARY KEY,
		folder_id INTEGER REFERENCES folders(id) ON DELETE CASCADE,
		filename TEXT NOT NULL,
		path TEXT NOT NULL UNIQUE,
		url_path TEXT UNIQUE,
		title TEXT,
		description TEXT,
		note TEXT,
		width INTEGER,
		height INTEGER,
		size_bytes BIGINT,
		blurhash TEXT,
		exif_data JSONB,
		hidden BOOLEAN DEFAULT FALSE,
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW(),
		taken_at TIMESTAMPTZ
	);

	CREATE INDEX IF NOT EXISTS idx_photos_folder ON photos(folder_id);
	CREATE INDEX IF NOT EXISTS idx_photos_path ON photos(path);
	CREATE INDEX IF NOT EXISTS idx_photos_url_path ON photos(url_path);
	CREATE INDEX IF NOT EXISTS idx_photos_hidden ON photos(hidden);

	ALTER TABLE folders DROP CONSTRAINT IF EXISTS folders_cover_photo_id_fkey;
	DO $$ BEGIN
		ALTER TABLE folders ADD CONSTRAINT folders_cover_photo_id_fkey 
		FOREIGN KEY (cover_photo_id) REFERENCES photos(id) ON DELETE SET NULL;
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

	DO $$ BEGIN
		ALTER TABLE photos ADD COLUMN IF NOT EXISTS url_path TEXT UNIQUE;
	EXCEPTION WHEN duplicate_column THEN NULL;
	END $$;

	CREATE INDEX IF NOT EXISTS idx_photos_url_path ON photos(url_path);

	CREATE TABLE IF NOT EXISTS photo_stats_cache (
		key TEXT PRIMARY KEY,
		data JSONB NOT NULL,
		updated_at TIMESTAMPTZ DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS staged_uploads (
		id TEXT PRIMARY KEY,
		seq BIGINT NOT NULL DEFAULT 0,
		filename TEXT NOT NULL,
		size_bytes BIGINT NOT NULL DEFAULT 0,
		width INTEGER NOT NULL DEFAULT 0,
		height INTEGER NOT NULL DEFAULT 0,
		is_raw BOOLEAN NOT NULL DEFAULT FALSE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_staged_seq ON staged_uploads(seq);

	ALTER TABLE staged_uploads ADD COLUMN IF NOT EXISTS rotation INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE photos ADD COLUMN IF NOT EXISTS rotation INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE staged_uploads ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT '';

	CREATE TABLE IF NOT EXISTS api_tokens (
		id SERIAL PRIMARY KEY,
		name TEXT NOT NULL,
		token_hash TEXT NOT NULL UNIQUE,
		prefix TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		last_used_at TIMESTAMPTZ,
		revoked_at TIMESTAMPTZ
	);

	ALTER TABLE photos ADD COLUMN IF NOT EXISTS edit_hash TEXT;

	CREATE TABLE IF NOT EXISTS photo_edits (
		photo_id INTEGER PRIMARY KEY REFERENCES photos(id) ON DELETE CASCADE,
		recipe JSONB NOT NULL,
		defaults_version INTEGER NOT NULL,
		recipe_revision BIGINT NOT NULL DEFAULT 1,
		desired_hash TEXT NOT NULL,
		published_hash TEXT,
		source_width INTEGER NOT NULL DEFAULT 0,
		source_height INTEGER NOT NULL DEFAULT 0,
		source_blurhash TEXT NOT NULL DEFAULT '',
		legacy_rotation INTEGER NOT NULL DEFAULT 0,
		render_state TEXT NOT NULL DEFAULT 'queued',
		render_error TEXT,
		render_attempts INTEGER NOT NULL DEFAULT 0,
		next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		published_at TIMESTAMPTZ,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_photo_edits_queue ON photo_edits(render_state, next_attempt_at)
		WHERE published_hash IS DISTINCT FROM desired_hash;

	CREATE TABLE IF NOT EXISTS photo_edit_history (
		id BIGSERIAL PRIMARY KEY,
		photo_id INTEGER NOT NULL REFERENCES photos(id) ON DELETE CASCADE,
		revision BIGINT NOT NULL,
		recipe JSONB NOT NULL,
		label TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_photo_edit_history_photo ON photo_edit_history(photo_id, id DESC);

	CREATE TABLE IF NOT EXISTS lens_profile_map (
		exif_lens TEXT PRIMARY KEY,
		profile TEXT NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS develop_presets (
		id SERIAL PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		recipe JSONB NOT NULL,
		modules TEXT[] NOT NULL DEFAULT '{}',
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	ALTER TABLE staged_uploads ADD COLUMN IF NOT EXISTS edit_rev BIGINT NOT NULL DEFAULT 0;

	ALTER TABLE photo_edits ADD COLUMN IF NOT EXISTS priority INTEGER NOT NULL DEFAULT 0;

	CREATE TABLE IF NOT EXISTS staged_edits (
		staged_id TEXT PRIMARY KEY REFERENCES staged_uploads(id) ON DELETE CASCADE,
		recipe JSONB NOT NULL,
		revision BIGINT NOT NULL DEFAULT 1,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	`
	_, err := db.pool.Exec(context.Background(), schema)
	return err
}
