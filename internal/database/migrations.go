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
	`
	_, err := db.pool.Exec(context.Background(), schema)
	return err
}
