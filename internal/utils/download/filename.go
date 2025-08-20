package download

import (
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// GetFileName retrieves the filename from HTTP response headers
func GetFileName(url string) string {
	// Try to get filename from headers first
	if filename := GetFileNameFromHeaders(url); filename != "" {
		return filename
	}

	// Fallback to URL parsing if headers don't provide filename
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		lastPart := parts[len(parts)-1]
		// Remove query parameters
		if idx := strings.Index(lastPart, "?"); idx != -1 {
			lastPart = lastPart[:idx]
		}
		if lastPart != "" {
			return lastPart
		}
	}
	return "downloaded_file"
}

// GetFileNameFromHeaders makes a HEAD request to get filename from response headers
func GetFileNameFromHeaders(url string) string {
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	// Try HEAD request first
	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return ""
	}

	// Add common headers that might be needed
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,ru;q=0.8")
	req.Header.Set("Accept-Encoding", "identity")

	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	// Check Content-Disposition header first (most reliable)
	if filename := ExtractFilenameFromContentDisposition(resp.Header.Get("Content-Disposition")); filename != "" {
		return filename
	}

	// Check Content-Type header for file extension
	if filename := ExtractFilenameFromContentType(resp.Header.Get("Content-Type")); filename != "" {
		return filename
	}

	// If HEAD request didn't work, try GET request (some servers only provide headers on GET)
	req, err = http.NewRequest("GET", url, nil)
	if err != nil {
		return ""
	}

	// Same headers as HEAD request
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,ru;q=0.8")
	req.Header.Set("Accept-Encoding", "identity")

	resp, err = client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	// Check Content-Disposition header from GET response
	if filename := ExtractFilenameFromContentDisposition(resp.Header.Get("Content-Disposition")); filename != "" {
		return filename
	}

	// Check Content-Type header from GET response
	if filename := ExtractFilenameFromContentType(resp.Header.Get("Content-Type")); filename != "" {
		return filename
	}

	return ""
}

// ExtractFilenameFromContentDisposition extracts filename from Content-Disposition header
func ExtractFilenameFromContentDisposition(contentDisposition string) string {
	if contentDisposition == "" {
		return ""
	}

	// Parse the Content-Disposition header
	_, params, err := mime.ParseMediaType(contentDisposition)
	if err != nil {
		return ""
	}

	// Look for filename parameter
	if filename, ok := params["filename"]; ok && filename != "" {
		// Clean the filename
		filename = strings.Trim(filename, `"'`)
		// Remove any path components for security
		return filepath.Base(filename)
	}

	// Look for filename* parameter (RFC 5987)
	if filename, ok := params["filename*"]; ok && filename != "" {
		// Parse RFC 5987 format: filename*=charset''encoded-filename
		if idx := strings.Index(filename, "''"); idx != -1 {
			filename = filename[idx+2:]
			// Clean the filename
			filename = strings.Trim(filename, `"'`)
			// Remove any path components for security
			return filepath.Base(filename)
		}
	}

	return ""
}

// ExtractFilenameFromContentType extracts a default filename based on Content-Type
func ExtractFilenameFromContentType(contentType string) string {
	if contentType == "" {
		return ""
	}

	// Parse the Content-Type header
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}

	// Map common media types to file extensions
	extensions := map[string]string{
		"video/mp4":                    ".mp4",
		"video/webm":                   ".webm",
		"video/ogg":                    ".ogv",
		"video/avi":                    ".avi",
		"video/quicktime":              ".mov",
		"video/x-msvideo":              ".avi",
		"video/x-ms-wmv":               ".wmv",
		"video/x-flv":                  ".flv",
		"video/3gpp":                   ".3gp",
		"video/3gpp2":                  ".3g2",
		"video/x-matroska":             ".mkv",
		"image/jpeg":                   ".jpg",
		"image/png":                    ".png",
		"image/gif":                    ".gif",
		"image/webp":                   ".webp",
		"image/svg+xml":                ".svg",
		"audio/mpeg":                   ".mp3",
		"audio/ogg":                    ".ogg",
		"audio/wav":                    ".wav",
		"audio/webm":                   ".weba",
		"application/pdf":              ".pdf",
		"application/zip":              ".zip",
		"application/x-rar-compressed": ".rar",
	}

	if ext, ok := extensions[mediaType]; ok {
		return fmt.Sprintf("file%s", ext)
	}

	return ""
}

// DetectFileType determines if a file is a photo or video based on its extension
//
// Returns "photo", "video", or "unknown"
func DetectFileType(filename string) string {
	// Get file extension and convert to lowercase
	ext := strings.ToLower(filepath.Ext(filename))
	// Remove the dot from extension
	if len(ext) > 0 {
		ext = ext[1:]
	}

	// Common photo extensions
	photoExtensions := map[string]bool{
		"jpg": true, "jpeg": true, "png": true, "gif": true,
		"bmp": true, "tiff": true, "tif": true, "webp": true,
		"svg": true, "ico": true, "raw": true, "cr2": true,
		"nef": true, "arw": true, "dng": true, "orf": true,
		"rw2": true, "pef": true, "srw": true, "heic": true,
		"heif": true,
	}

	// Common video extensions
	videoExtensions := map[string]bool{
		"mp4": true, "avi": true, "mov": true, "wmv": true,
		"flv": true, "webm": true, "mkv": true, "m4v": true,
		"3gp": true, "ogv": true, "mpg": true, "mpeg": true,
		"ts": true, "vob": true, "asf": true, "rm": true,
		"rmvb": true, "f4v": true, "swf": true, "mts": true,
		"m2ts": true,
	}

	if photoExtensions[ext] {
		return "photo"
	} else if videoExtensions[ext] {
		return "video"
	}
	return "unknown"
}
