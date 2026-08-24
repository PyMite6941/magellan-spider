package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ── HTML parsing regexes ───────────────────────────────────────────────────────

var (
	reTitleTag  = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reScriptCSS = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	reHTMLTags  = regexp.MustCompile(`<[^>]+>`)
	reImgTag    = regexp.MustCompile(`(?i)<img[^>]+>`)
	reSrcAttr   = regexp.MustCompile(`(?i)\bsrc=["']([^"'\s>]+)["']`)
	reAltAttr   = regexp.MustCompile(`(?i)\balt=["']([^"']*)["']`)
	reAnchorHref = regexp.MustCompile(`(?i)<a\b[^>]*\bhref=["']([^"'\s>]+)["']`)
	reWhitespace = regexp.MustCompile(`\s+`)
)

// downloadable file extensions we want to record
var fileExtensions = map[string]bool{
	"pdf": true, "doc": true, "docx": true, "xls": true, "xlsx": true,
	"ppt": true, "pptx": true, "zip": true, "tar": true, "gz": true,
	"csv": true, "json": true, "xml": true, "txt": true, "md": true,
	"mp3": true, "mp4": true, "mov": true, "avi": true, "wav": true,
	"epub": true, "odt": true, "ods": true,
}

// ── Text extraction ────────────────────────────────────────────────────────────

func extractTitle(html string) string {
	m := reTitleTag.FindStringSubmatch(html)
	if len(m) < 2 {
		return ""
	}
	t := reHTMLTags.ReplaceAllString(m[1], "")
	t = reWhitespace.ReplaceAllString(t, " ")
	return strings.TrimSpace(t)
}

func extractSnippet(html string) string {
	text := reScriptCSS.ReplaceAllString(html, " ")
	text = reHTMLTags.ReplaceAllString(text, " ")
	text = reWhitespace.ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)
	if len(text) > 300 {
		cut := text[:300]
		if idx := strings.LastIndex(cut, " "); idx > 200 {
			cut = cut[:idx]
		}
		return cut + "..."
	}
	return text
}

// ── URL resolution ─────────────────────────────────────────────────────────────

func resolveURL(baseURL, href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		parsed, err := url.Parse(href)
		if err != nil || parsed.Host == "" {
			return ""
		}
		return href
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	ref, err := url.Parse(href)
	if err != nil {
		return ""
	}
	resolved := base.ResolveReference(ref)
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return ""
	}
	return resolved.String()
}

// ── robots.txt ────────────────────────────────────────────────────────────────

func fetchRobotsDisallowed(baseURL string) []string {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", baseURL+"/robots.txt", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		return nil
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	var disallowed []string
	applies := false
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "user-agent:"):
			agent := strings.TrimSpace(line[len("user-agent:"):])
			applies = agent == "*" || strings.Contains(strings.ToLower(agent), "magellan")
		case applies && strings.HasPrefix(lower, "disallow:"):
			p := strings.TrimSpace(line[len("disallow:"):])
			if p != "" {
				disallowed = append(disallowed, strings.ToLower(p))
			}
		}
	}
	return disallowed
}

func checkRobotsTxt(urlStr string) bool {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return true
	}
	base := parsed.Scheme + "://" + parsed.Host

	mu.Lock()
	disallowed, cached := robotsCache[base]
	mu.Unlock()

	if !cached {
		disallowed = fetchRobotsDisallowed(base)
		mu.Lock()
		robotsCache[base] = disallowed
		mu.Unlock()
	}

	path := strings.ToLower(parsed.Path)
	for _, d := range disallowed {
		if d != "" && strings.HasPrefix(path, d) {
			return false
		}
	}
	return true
}

// ── Database writes ───────────────────────────────────────────────────────────

func storePage(urlStr, domain, path, contentHash string, statusCode int, responseTimeMs int64, contentType string, contentLength int64, kwStr string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := db.Exec(`
		INSERT OR IGNORE INTO pages
			(url, title, snippet, domain, path, content_hash, last_crawled,
			 status_code, response_time_ms, content_type, content_length, keywords)
		VALUES (?, '', '', ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		urlStr, domain, path, contentHash, now, statusCode, responseTimeMs, contentType, contentLength, kwStr,
	)
	if err != nil {
		return 0, err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		var id int64
		if e := db.QueryRow("SELECT id FROM pages WHERE url = ?", urlStr).Scan(&id); e != nil {
			return 0, e
		}
		return id, nil
	}
	return result.LastInsertId()
}

func storePageContent(pageID int64, content string) {
	title := extractTitle(content)
	snippet := extractSnippet(content)

	_, err := db.Exec(
		"UPDATE pages SET title = ?, snippet = ? WHERE id = ? AND title = ''",
		title, snippet, pageID,
	)
	if err != nil {
		fmt.Printf("Warning: could not update page title/snippet for id %d: %v\n", pageID, err)
	}

	db.Exec(
		"INSERT OR REPLACE INTO pages_content (page_id, content) VALUES (?, ?)",
		pageID, content,
	)
}

func extractAndStoreImages(pageID int64, content, baseURL string) {
	stmt, err := db.Prepare("INSERT OR IGNORE INTO images (url, alt, source) VALUES (?, ?, ?)")
	if err != nil {
		return
	}
	defer stmt.Close()

	for _, imgTag := range reImgTag.FindAllString(content, -1) {
		srcM := reSrcAttr.FindStringSubmatch(imgTag)
		if len(srcM) < 2 {
			continue
		}
		imgURL := resolveURL(baseURL, srcM[1])
		if imgURL == "" || strings.HasPrefix(imgURL, "data:") {
			continue
		}
		alt := ""
		if altM := reAltAttr.FindStringSubmatch(imgTag); len(altM) >= 2 {
			alt = altM[1]
		}
		stmt.Exec(imgURL, alt, baseURL)
	}
}

func extractAndStoreFiles(pageID int64, content, baseURL string) {
	stmt, err := db.Prepare("INSERT OR IGNORE INTO files (url, filename, filetype, source) VALUES (?, ?, ?, ?)")
	if err != nil {
		return
	}
	defer stmt.Close()

	for _, m := range reAnchorHref.FindAllStringSubmatch(content, -1) {
		if len(m) < 2 {
			continue
		}
		href := strings.TrimSpace(m[1])
		if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "javascript:") {
			continue
		}
		fileURL := resolveURL(baseURL, href)
		if fileURL == "" {
			continue
		}
		// Strip query string to get extension
		clean := strings.Split(strings.ToLower(fileURL), "?")[0]
		parts := strings.Split(clean, ".")
		if len(parts) < 2 {
			continue
		}
		ext := parts[len(parts)-1]
		if !fileExtensions[ext] {
			continue
		}
		urlParts := strings.Split(clean, "/")
		filename := urlParts[len(urlParts)-1]
		stmt.Exec(fileURL, filename, ext, baseURL)
	}
}

func extractLinksAndAddToQueue(content, baseURL string, verbose bool) {
	kwStr := strings.Join(keywords, ",")
	for _, m := range reAnchorHref.FindAllStringSubmatch(content, -1) {
		if len(m) < 2 {
			continue
		}
		href := strings.TrimSpace(m[1])
		if href == "" || strings.HasPrefix(href, "#") ||
			strings.HasPrefix(href, "javascript:") || strings.HasPrefix(href, "mailto:") {
			continue
		}
		resolved := resolveURL(baseURL, href)
		if resolved == "" {
			continue
		}
		addToQueue(resolved, 0, kwStr)
	}
}

// ── Keyword extraction ─────────────────────────────────────────────────────────

var stopWords = map[string]bool{
	"about": true, "after": true, "again": true, "being": true, "could": true,
	"every": true, "first": true, "found": true, "great": true, "group": true,
	"large": true, "learn": true, "never": true, "often": true, "other": true,
	"place": true, "point": true, "right": true, "small": true, "sound": true,
	"still": true, "study": true, "their": true, "there": true, "these": true,
	"thing": true, "think": true, "three": true, "under": true, "water": true,
	"where": true, "which": true, "while": true, "world": true, "would": true,
	"write": true, "years": true, "those": true, "since": true, "pages": true,
	"https": true, "http":  true, "html":  true, "class": true,
}

func extractKeywordsFromContent(content, urlStr string) []string {
	var kw []string

	// Title words
	for _, word := range strings.Fields(extractTitle(content)) {
		w := strings.ToLower(strings.Trim(word, `.,!?;:"'()-[]`))
		if len(w) > 4 && !stopWords[w] {
			kw = append(kw, w)
		}
	}

	// URL path segments
	if parsed, err := url.Parse(urlStr); err == nil {
		for _, seg := range strings.Split(parsed.Path, "/") {
			seg = strings.ToLower(strings.Trim(seg, "-_"))
			if len(seg) > 4 && !strings.Contains(seg, ".") && !stopWords[seg] {
				kw = append(kw, seg)
			}
		}
	}

	return removeDuplicates(kw)
}

func removeDuplicates(slice []string) []string {
	seen := make(map[string]bool, len(slice))
	out := make([]string, 0, len(slice))
	for _, s := range slice {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
