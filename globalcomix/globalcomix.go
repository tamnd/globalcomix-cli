// Package globalcomix is the library behind the gc command line:
// the HTTP client, HTML parsing, and typed data models for globalcomix.com.
//
// GlobalComix serves server-rendered HTML at legacy /a/ paths.
// The client parses these pages using Go's x/net/html package.
// No API key or authentication is required for public catalog pages.
package globalcomix

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

// Host is the GlobalComix hostname.
const Host = "globalcomix.com"

// BaseURL is the root every request is built from.
const BaseURL = "https://" + Host

// DefaultUserAgent identifies the client to GlobalComix.
const DefaultUserAgent = "gc-cli/dev (+https://github.com/tamnd/globalcomix-cli)"

// ErrNotFound is returned when a comic or resource is not found.
var ErrNotFound = errors.New("not found")

// Config holds constructor parameters for Client.
type Config struct {
	BaseURL   string
	UserAgent string
	Rate      time.Duration
	Retries   int
	Timeout   time.Duration
}

// DefaultConfig returns sensible defaults for GlobalComix.
func DefaultConfig() Config {
	return Config{
		BaseURL:   BaseURL,
		UserAgent: DefaultUserAgent,
		Rate:      300 * time.Millisecond,
		Retries:   3,
		Timeout:   30 * time.Second,
	}
}

// Client is a rate-limited HTTP client for GlobalComix.
type Client struct {
	cfg  Config
	http *http.Client
	mu   sync.Mutex
	last time.Time
}

// NewClient returns a Client configured with cfg.
func NewClient(cfg Config) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: cfg.Timeout},
	}
}

// pace blocks until at least cfg.Rate has elapsed since the last request.
func (c *Client) pace() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cfg.Rate <= 0 {
		return
	}
	if wait := c.cfg.Rate - time.Since(c.last); wait > 0 {
		time.Sleep(wait)
	}
	c.last = time.Now()
}

// get fetches a URL with retries on transient errors.
func (c *Client) get(ctx context.Context, rawURL string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.cfg.Retries; attempt++ {
		if attempt > 0 {
			wait := time.Duration(attempt) * 500 * time.Millisecond
			if wait > 5*time.Second {
				wait = 5 * time.Second
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}
		body, retry, err := c.do(ctx, rawURL)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}
	}
	return nil, fmt.Errorf("get %s: %w", rawURL, lastErr)
}

func (c *Client) do(ctx context.Context, rawURL string) ([]byte, bool, error) {
	c.pace()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("http %d", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("http %d", resp.StatusCode)
	}

	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, true, err
	}
	return b, false, nil
}

// ---- Types ----

// Comic is one comic entry in a listing or search result.
type Comic struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Cover       string `json:"cover"`
	Language    string `json:"language"`
	Genres      string `json:"genres"`
	Authors     string `json:"authors"`
	URL         string `json:"url"`
}

// Chapter is one chapter entry in a comic's chapter list.
type Chapter struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Number      string `json:"number"`
	PublishedAt string `json:"published_at"`
	URL         string `json:"url"`
}

// ComicDetail is the full detail for a comic page.
type ComicDetail struct {
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Cover       string    `json:"cover"`
	Language    string    `json:"language"`
	Genres      string    `json:"genres"`
	Authors     string    `json:"authors"`
	Chapters    []Chapter `json:"chapters"`
	URL         string    `json:"url"`
}

// ---- HTML Parsing ----

// parseMeta extracts Open Graph and standard meta content values from an HTML doc.
func parseMeta(doc *html.Node) map[string]string {
	out := make(map[string]string)
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "meta" {
			var prop, name, content string
			for _, a := range n.Attr {
				switch a.Key {
				case "property":
					prop = a.Val
				case "name":
					name = a.Val
				case "content":
					content = a.Val
				}
			}
			if prop != "" {
				out[prop] = content
			}
			if name != "" {
				out[name] = content
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out
}

// comicSlugFromHref extracts a comic slug from an href like /a/comics/some-slug
// or /a/comics/some-slug/chapter-1.
func comicSlugFromHref(href string) (string, bool) {
	const prefix = "/a/comics/"
	if !strings.HasPrefix(href, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(href, prefix)
	// Take only the first path segment (the slug itself, not chapter sub-paths
	// that have a "/" after the slug).
	parts := strings.SplitN(rest, "/", 2)
	slug := strings.TrimSpace(parts[0])
	if slug == "" || slug == "top" || slug == "new" || slug == "search" {
		return "", false
	}
	// If there's a second segment, it's a chapter link, not a comic listing link.
	// We still return the slug so callers can use it.
	return slug, true
}

// parseComicCards parses comic listing HTML and extracts Comic stubs.
// It looks for anchor elements whose href matches the /a/comics/<slug> pattern,
// deduplicating by slug.
func parseComicCards(body []byte) ([]Comic, error) {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}

	meta := parseMeta(doc)
	pageURL := meta["og:url"]
	if pageURL == "" {
		pageURL = BaseURL
	}

	seen := make(map[string]struct{})
	var out []Comic

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, a := range n.Attr {
				if a.Key == "href" {
					slug, ok := comicSlugFromHref(a.Val)
					if !ok {
						break
					}
					// Only capture direct comic listing links (no sub-path).
					if strings.Count(strings.TrimPrefix(a.Val, "/a/comics/"), "/") > 0 {
						break
					}
					if _, seen2 := seen[slug]; seen2 {
						break
					}
					seen[slug] = struct{}{}
					title := nodeText(n)
					if title == "" {
						for _, attr := range n.Attr {
							if attr.Key == "aria-label" || attr.Key == "title" {
								title = attr.Val
								break
							}
						}
					}
					out = append(out, Comic{
						Slug:  slug,
						Title: title,
						URL:   BaseURL + "/a/comics/" + slug,
					})
					break
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	_ = pageURL
	return out, nil
}

// parseChapters parses a comic detail page and extracts its chapter list.
func parseChapters(body []byte, comicSlug string) ([]Chapter, error) {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}

	prefix := "/a/comics/" + comicSlug + "/"
	seen := make(map[string]struct{})
	var out []Chapter

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, a := range n.Attr {
				if a.Key == "href" && strings.HasPrefix(a.Val, prefix) {
					chSlug := strings.TrimPrefix(a.Val, prefix)
					chSlug = strings.Trim(chSlug, "/")
					if chSlug == "" {
						break
					}
					if _, dup := seen[chSlug]; dup {
						break
					}
					seen[chSlug] = struct{}{}
					text := strings.TrimSpace(nodeText(n))
					num, title := parseChapterText(text)
					out = append(out, Chapter{
						Slug:   chSlug,
						Number: num,
						Title:  title,
						URL:    BaseURL + a.Val,
					})
					break
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out, nil
}

// parseChapterText splits "Chapter 1 - Some Title" into ("1", "Some Title").
func parseChapterText(s string) (num, title string) {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	for _, prefix := range []string{"chapter ", "ch. ", "ch "} {
		if idx := strings.Index(lower, prefix); idx >= 0 {
			rest := strings.TrimSpace(s[idx+len(prefix):])
			// rest might be "1 - Some Title" or "1: Some Title" or just "1"
			for _, sep := range []string{" - ", ": ", " "} {
				if i := strings.Index(rest, sep); i >= 0 {
					num = strings.TrimSpace(rest[:i])
					title = strings.TrimSpace(rest[i+len(sep):])
					return
				}
			}
			num = rest
			return
		}
	}
	// No "chapter" keyword; use the full text as title.
	title = s
	return
}

// nodeText returns the concatenated text content of a node and its descendants.
func nodeText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(sb.String())
}

// parseComicDetail extracts the main metadata from a comic detail page.
func parseComicDetail(body []byte, slug string) (*ComicDetail, error) {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}
	meta := parseMeta(doc)
	chapters, _ := parseChapters(body, slug)
	return &ComicDetail{
		Slug:        slug,
		Title:       meta["og:title"],
		Description: meta["og:description"],
		Cover:       meta["og:image"],
		URL:         BaseURL + "/a/comics/" + slug,
		Chapters:    chapters,
	}, nil
}

// ---- API Methods ----

// Trending fetches trending comics from the listing page.
func (c *Client) Trending(ctx context.Context, page int) ([]Comic, error) {
	if page < 1 {
		page = 1
	}
	rawURL := fmt.Sprintf("%s/a/comics/top?language=en&page=%d", c.cfg.BaseURL, page)
	body, err := c.get(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	return parseComicCards(body)
}

// New fetches newly added comics from the listing page.
func (c *Client) New(ctx context.Context, page int) ([]Comic, error) {
	if page < 1 {
		page = 1
	}
	rawURL := fmt.Sprintf("%s/a/comics/new?language=en&page=%d", c.cfg.BaseURL, page)
	body, err := c.get(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	return parseComicCards(body)
}

// Search searches for comics by query.
func (c *Client) Search(ctx context.Context, q string, page int) ([]Comic, error) {
	if page < 1 {
		page = 1
	}
	rawURL := fmt.Sprintf("%s/a/search?query=%s&page=%d", c.cfg.BaseURL, urlEncode(q), page)
	body, err := c.get(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	return parseComicCards(body)
}

// GetComic fetches the detail page for a comic by slug.
func (c *Client) GetComic(ctx context.Context, slug string) (*ComicDetail, error) {
	rawURL := fmt.Sprintf("%s/a/comics/%s", c.cfg.BaseURL, slug)
	body, err := c.get(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	return parseComicDetail(body, slug)
}

// GetChapters fetches the chapter list for a comic by slug.
func (c *Client) GetChapters(ctx context.Context, slug string) ([]Chapter, error) {
	detail, err := c.GetComic(ctx, slug)
	if err != nil {
		return nil, err
	}
	return detail.Chapters, nil
}

// urlEncode is a minimal URL query-string encoder.
func urlEncode(s string) string {
	var sb strings.Builder
	for _, b := range []byte(s) {
		if isURLSafe(b) {
			sb.WriteByte(b)
		} else if b == ' ' {
			sb.WriteByte('+')
		} else {
			fmt.Fprintf(&sb, "%%%02X", b)
		}
	}
	return sb.String()
}

func isURLSafe(b byte) bool {
	return (b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') ||
		b == '-' || b == '_' || b == '.' || b == '~'
}
