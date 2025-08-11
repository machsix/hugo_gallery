# Photo Folder Watcher & Hugo Integration

## Features

- Watches a folder (recursively) for new image subfolders.
- If a folder contains more than 3 images (`.jpg`, `.png`...), generates a Hugo-compatible Markdown post.
- Categories are multi-level, matching subfolder structure.
- Markdown files use SHA of folder name and lazy-load images.
- Tracks posts in SQLite; deletes post if folder is deleted.
- Calls Hugo to rebuild after changes.
- Serves Hugo static site on configurable port.
- All settings via `config.ini`.

## Usage

1. **Clone or copy files**  
   Place all `.go` files and `config.ini` in a folder.

2. **Install dependencies**  
   ```bash
   go mod init github.com/yourusername/photo-watcher
   go get github.com/fsnotify/fsnotify gopkg.in/ini.v1 github.com/mattn/go-sqlite3
   ```

3. **Configure**  
   Edit `config.ini` for folder paths, Hugo locations, etc.

4. **Build**  
   ```bash
   go build -o photo-watcher .
   ```

5. **Prepare Hugo site**  
   - Install Hugo (`brew install hugo`, `apt install hugo`, etc.).
   - `hugo new site hugosite`
   - Copy `archetypes/photo.md` to your Hugo site’s archetypes.

6. **Run**  
   ```bash
   ./photo-watcher
   ```

## Hugo Config Example

In your Hugo site’s `config.toml`:

```toml
contentDir = "content"
publishDir = "../public"
```

## Customizing

- Edit `archetypes/photo.md` for post template.
- Adjust `photo_extensions` in `config.ini` as needed.

## Notes

- For production, consider a robust natural sort for images.
- You may change the content directory, output directory, and more in configs.
