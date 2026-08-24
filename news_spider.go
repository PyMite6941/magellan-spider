package main

import (
	"database/sql"
	"flag"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// NewsSource defines a news outlet with its known political bias classification.
// Bias ratings follow AllSides Media Bias Ratings methodology.
type NewsSource struct {
	Name   string
	RSS    string
	Bias   string // "left" | "center-left" | "center" | "center-right" | "right"
	Domain string
}

// newsSources has exactly 2 outlets per bias tier for balanced representation.
var newsSources = []NewsSource{
	// Left
	{Name: "The Guardian", RSS: "https://www.theguardian.com/world/rss", Bias: "left", Domain: "theguardian.com"},
	{Name: "Democracy Now", RSS: "https://www.democracynow.org/democracynow.rss", Bias: "left", Domain: "democracynow.org"},
	// Center-Left
	{Name: "NPR", RSS: "https://feeds.npr.org/1001/rss.xml", Bias: "center-left", Domain: "npr.org"},
	{Name: "Politico", RSS: "https://rss.politico.com/politics-news.xml", Bias: "center-left", Domain: "politico.com"},
	// Center
	{Name: "BBC News", RSS: "http://feeds.bbci.co.uk/news/rss.xml", Bias: "center", Domain: "bbc.com"},
	{Name: "PBS NewsHour", RSS: "https://www.pbs.org/newshour/feeds/rss/headlines", Bias: "center", Domain: "pbs.org"},
	// Center-Right
	{Name: "The Economist", RSS: "https://www.economist.com/rss/the_world_this_week_rss.xml", Bias: "center-right", Domain: "economist.com"},
	{Name: "The Hill", RSS: "https://thehill.com/feed/", Bias: "center-right", Domain: "thehill.com"},
	// Right
	{Name: "Fox News", RSS: "https://moxie.foxnews.com/google-publisher/latest.xml", Bias: "right", Domain: "foxnews.com"},
	{Name: "National Review", RSS: "https://www.nationalreview.com/feed/", Bias: "right", Domain: "nationalreview.com"},
}

// ── RSS parsing ────────────────────────────────────────────────────────────────
// We parse the raw bytes manually rather than relying on encoding/xml so that
// we reliably capture namespace-prefixed elements like <content:encoded> and
// handle CDATA sections that many feeds use for full article text.

var (
	reItems          = regexp.MustCompile(`(?s)<item[^>]*>(.*?)</item>`)
	reTagContent     = func(tag string) *regexp.Regexp {
		return regexp.MustCompile(`(?s)<` + tag + `[^>]*>(.*?)</` + tag + `>`)
	}
	reCDATA          = regexp.MustCompile(`(?s)<!\[CDATA\[(.*?)]]>`)
	reContentEncoded = regexp.MustCompile(`(?s)<content:encoded[^>]*>(.*?)</content:encoded>`)
	reHTMLEntities   = strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">",
		"&quot;", `"`, "&#39;", "'", "&nbsp;", " ",
		"&mdash;", "—", "&ndash;", "–", "&hellip;", "…",
	)
)

type parsedItem struct {
	Title     string
	Link      string
	Snippet   string // short summary (≤300 chars) for list view
	Content   string // full readable text for expanded view
	PubDate   string
}

// extractField pulls the inner text of the first matching tag, unwrapping CDATA.
func extractField(raw, tag string) string {
	re := reTagContent(tag)
	m := re.FindStringSubmatch(raw)
	if len(m) < 2 {
		return ""
	}
	val := m[1]
	if cdataM := reCDATA.FindStringSubmatch(val); len(cdataM) >= 2 {
		val = cdataM[1]
	}
	return strings.TrimSpace(val)
}

// cleanText strips HTML tags and decodes entities, collapsing whitespace.
func cleanText(s string) string {
	// Remove script/style blocks
	s = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`).ReplaceAllString(s, " ")
	// Remove all tags
	s = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, " ")
	// Decode entities
	s = reHTMLEntities.Replace(s)
	// Collapse whitespace
	s = regexp.MustCompile(`[ \t]+`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(`\n{3,}`).ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func snippet(text string) string {
	if len(text) <= 300 {
		return text
	}
	cut := text[:300]
	if idx := strings.LastIndex(cut, " "); idx > 200 {
		cut = cut[:idx]
	}
	return cut + "…"
}

func parseItems(body []byte) []parsedItem {
	matches := reItems.FindAllSubmatch(body, -1)
	items := make([]parsedItem, 0, len(matches))

	for _, m := range matches {
		raw := string(m[1])

		title := cleanText(extractField(raw, "title"))
		link  := strings.TrimSpace(extractField(raw, "link"))
		if link == "" {
			// Some feeds put the URL in <guid isPermaLink="true">
			link = strings.TrimSpace(extractField(raw, "guid"))
		}
		if link == "" || !strings.HasPrefix(link, "http") {
			continue
		}

		pubDate := extractField(raw, "pubDate")
		if pubDate == "" {
			pubDate = extractField(raw, "dc:date")
		}

		// Full content: prefer content:encoded (often has paragraphs of text),
		// fall back to description.
		fullHTML := ""
		if ce := reContentEncoded.FindStringSubmatch(raw); len(ce) >= 2 {
			val := ce[1]
			if cdataM := reCDATA.FindStringSubmatch(val); len(cdataM) >= 2 {
				val = cdataM[1]
			}
			fullHTML = val
		}
		if fullHTML == "" {
			fullHTML = extractField(raw, "description")
		}

		fullText := cleanText(fullHTML)

		items = append(items, parsedItem{
			Title:   title,
			Link:    link,
			Snippet: snippet(fullText),
			Content: fullText,
			PubDate: pubDate,
		})
	}
	return items
}

func fetchAndParseRSS(rawURL string) ([]parsedItem, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	items := parseItems(body)
	if len(items) == 0 {
		return nil, fmt.Errorf("no <item> elements found in feed")
	}
	return items, nil
}

// ── Flags ──────────────────────────────────────────────────────────────────────

func addNewsFlag() {
	flag.Bool("news", false, "Crawl news RSS feeds with political-bias classification")
	flag.Int("news-limit", 10, "Max articles to fetch per news source (equal per bias tier)")
}

func handleNewsSearch() bool {
	f := flag.Lookup("news")
	if f == nil || !f.Value.(flag.Getter).Get().(bool) {
		return false
	}
	limitFlag := flag.Lookup("news-limit")
	limit := 10
	if limitFlag != nil {
		limit = limitFlag.Value.(flag.Getter).Get().(int)
	}
	runNewsCrawler(db, limit)
	return true
}

// ── Database ───────────────────────────────────────────────────────────────────

func initNewsTable(d *sql.DB) error {
	_, err := d.Exec(`
		CREATE TABLE IF NOT EXISTS news (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			url           TEXT UNIQUE NOT NULL,
			title         TEXT,
			snippet       TEXT,
			content       TEXT,
			source        TEXT,
			source_domain TEXT,
			bias          TEXT,
			published_at  TEXT,
			crawled_at    TEXT
		)
	`)
	if err != nil {
		return err
	}
	// Migration: add content column to existing databases
	d.Exec(`ALTER TABLE news ADD COLUMN content TEXT`)
	return nil
}

func crawlNewsSource(d *sql.DB, src NewsSource, limit int) (int, error) {
	items, err := fetchAndParseRSS(src.RSS)
	if err != nil {
		return 0, err
	}

	stmt, err := d.Prepare(`
		INSERT OR IGNORE INTO news
			(url, title, snippet, content, source, source_domain, bias, published_at, crawled_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	count := 0
	for i, item := range items {
		if i >= limit {
			break
		}
		_, execErr := stmt.Exec(
			item.Link, item.Title, item.Snippet, item.Content,
			src.Name, src.Domain, src.Bias, item.PubDate, now,
		)
		if execErr == nil {
			count++
		}
	}
	return count, nil
}

func runNewsCrawler(d *sql.DB, articlesPerSource int) {
	if err := initNewsTable(d); err != nil {
		fmt.Printf("[news] DB init error: %v\n", err)
		return
	}

	fmt.Printf("[news] Crawling %d sources (%d articles each, %d per bias tier)\n",
		len(newsSources), articlesPerSource, articlesPerSource*2)

	totals := make(map[string]int)
	for _, src := range newsSources {
		n, err := crawlNewsSource(d, src, articlesPerSource)
		if err != nil {
			fmt.Printf("[news]  %-22s %-14s  ERROR: %v\n", src.Name, "("+src.Bias+")", err)
		} else {
			fmt.Printf("[news]  %-22s %-14s  +%d articles\n", src.Name, "("+src.Bias+")", n)
			totals[src.Bias] += n
		}
		time.Sleep(600 * time.Millisecond)
	}

	fmt.Println("[news] Summary by bias tier:")
	for _, tier := range []string{"left", "center-left", "center", "center-right", "right"} {
		fmt.Printf("[news]   %-14s  %d new articles\n", tier, totals[tier])
	}
	fmt.Println("[news] Done.")
}
