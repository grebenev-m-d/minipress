CREATE TABLE IF NOT EXISTS pages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    public_id TEXT UNIQUE NOT NULL,
    edit_token TEXT UNIQUE NOT NULL,
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    author TEXT,
    is_published INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS images (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file_name TEXT NOT NULL UNIQUE,
    uploaded_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    orphaned_at DATETIME
);

CREATE TABLE IF NOT EXISTS image_refs (
    image_id INTEGER NOT NULL REFERENCES images(id) ON DELETE CASCADE,
    page_id INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    PRIMARY KEY (image_id, page_id)
) WITHOUT ROWID;

CREATE INDEX IF NOT EXISTS idx_image_refs_page ON image_refs(page_id);
CREATE INDEX IF NOT EXISTS idx_images_orphaned ON images(orphaned_at) WHERE orphaned_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_pages_drafts ON pages(created_at) WHERE is_published = 0;