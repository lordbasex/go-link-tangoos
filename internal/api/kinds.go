package api

import (
	"path"
	"strings"
)

// KindOf clasifica un archivo por su extensión (mismo criterio que el FileKind de TS).
func KindOf(name string) string {
	switch strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".") {
	case "jpg", "jpeg", "png", "heic", "heif", "webp", "gif", "avif", "bmp", "tif", "tiff", "raw", "cr2", "nef", "dng":
		return "image"
	case "mp4", "mkv", "mov", "avi", "webm", "m4v", "mpg", "mpeg", "wmv", "m2ts":
		return "video"
	case "flac", "mp3", "m4a", "wav", "ogg", "opus", "aac", "alac":
		return "audio"
	case "pdf":
		return "pdf"
	case "doc", "docx", "odt", "pages", "rtf":
		return "doc"
	case "xls", "xlsx", "ods", "csv", "numbers":
		return "sheet"
	case "go", "ts", "tsx", "js", "json", "yaml", "yml", "sh", "py", "c", "h", "rs", "java", "html", "css", "plist", "xml", "toml":
		return "code"
	case "zip", "tar", "gz", "tgz", "7z", "rar", "xz", "zst", "iso", "dmg", "sparsebundle":
		return "archive"
	case "txt", "md", "log", "ini", "conf":
		return "text"
	}
	return "other"
}
