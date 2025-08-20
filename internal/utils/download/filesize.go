package download

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// GetFileSize retrieves the file size in bytes from a given URL.
func GetFileSize(url string) (int64, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	// Common headers for both requests
	setHeaders := func(req *http.Request) {
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "*/*")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		req.Header.Set("Accept-Encoding", "identity") // Prevent compression for accurate size
		req.Header.Set("Connection", "keep-alive")
	}

	// Method 1: Try HEAD request
	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create HEAD request: %w", err)
	}
	setHeaders(req)
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")

	if resp, err := client.Do(req); err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			if size, ok := parseContentLength(resp); ok {
				return size, nil
			}
		}
	}

	// Method 2: Try GET request
	req, err = http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create GET request: %w", err)
	}
	setHeaders(req)
	req.Header.Set("Referer", "https://ok.ru/")

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("GET request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	// Check Content-Length from GET response
	if size, ok := parseContentLength(resp); ok {
		return size, nil
	}

	// Method 3: Read the actual content to determine size
	return readContentSize(resp.Body)
}

func readContentSize(body io.ReadCloser) (int64, error) {
	const maxSize = 500 * 1024 * 1024 // 500MB limit
	bytesRead, err := io.Copy(io.Discard, io.LimitReader(body, maxSize))
	if err != nil {
		return 0, fmt.Errorf("failed to read content: %w", err)
	}

	if bytesRead == maxSize {
		return bytesRead, fmt.Errorf("file size exceeds 500MB limit (downloaded: %s)", ByteCountBinary(bytesRead))
	}

	return bytesRead, nil
}

func ByteCountBinary(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// parseContentLength is a helper to parse Content-Length with validation
func parseContentLength(resp *http.Response) (int64, bool) {
	contentLength := strings.TrimSpace(resp.Header.Get("Content-Length"))
	if contentLength == "" || contentLength == "0" {
		return 0, false
	}

	bytes, err := strconv.ParseInt(contentLength, 10, 64)
	if err != nil {
		return 0, false
	}

	// Additional validation - reject negative
	if bytes < 0 { // 100GB limit
		return 0, false
	}

	return bytes, true
}
