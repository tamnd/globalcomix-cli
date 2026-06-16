package globalcomix_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/globalcomix-cli/globalcomix"
)

func TestDefaultConfig(t *testing.T) {
	cfg := globalcomix.DefaultConfig()
	if cfg.BaseURL == "" {
		t.Error("BaseURL is empty")
	}
	if cfg.UserAgent == "" {
		t.Error("UserAgent is empty")
	}
	if cfg.Rate <= 0 {
		t.Errorf("Rate = %v, want > 0", cfg.Rate)
	}
	if cfg.Retries <= 0 {
		t.Errorf("Retries = %d, want > 0", cfg.Retries)
	}
	if cfg.Timeout <= 0 {
		t.Errorf("Timeout = %v, want > 0", cfg.Timeout)
	}
}

func TestNewClientNotNil(t *testing.T) {
	c := globalcomix.NewClient(globalcomix.DefaultConfig())
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
}

func TestHostConstant(t *testing.T) {
	if globalcomix.Host != "globalcomix.com" {
		t.Errorf("Host = %q, want globalcomix.com", globalcomix.Host)
	}
}

func TestErrNotFound(t *testing.T) {
	if globalcomix.ErrNotFound == nil {
		t.Error("ErrNotFound is nil")
	}
	if globalcomix.ErrNotFound.Error() == "" {
		t.Error("ErrNotFound has empty message")
	}
}

func TestGetOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("request carried no User-Agent")
		}
		_, _ = w.Write(listingHTML([]comicCard{
			{Slug: "test-comic", Title: "Test Comic"},
		}))
	}))
	defer srv.Close()

	cfg := globalcomix.DefaultConfig()
	cfg.BaseURL = srv.URL
	cfg.Rate = 0
	c := globalcomix.NewClient(cfg)

	comics, err := c.Trending(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(comics) == 0 {
		t.Fatal("expected at least one comic")
	}
}

func TestGetNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	cfg := globalcomix.DefaultConfig()
	cfg.BaseURL = srv.URL
	cfg.Rate = 0
	cfg.Retries = 0
	c := globalcomix.NewClient(cfg)

	_, err := c.GetComic(context.Background(), "nonexistent-slug")
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
}

func TestGet503Retry(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "server error", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(listingHTML(nil))
	}))
	defer srv.Close()

	cfg := globalcomix.DefaultConfig()
	cfg.BaseURL = srv.URL
	cfg.Rate = 0
	cfg.Retries = 2
	c := globalcomix.NewClient(cfg)

	_, err := c.Trending(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error after retry: %v", err)
	}
	if calls < 2 {
		t.Errorf("expected at least 2 calls, got %d", calls)
	}
}

func TestContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := globalcomix.DefaultConfig()
	cfg.Rate = 0
	cfg.Retries = 0
	c := globalcomix.NewClient(cfg)

	_, err := c.Trending(ctx, 1)
	if err == nil {
		t.Error("expected error with cancelled context, got nil")
	}
}

func TestPaceGap(t *testing.T) {
	var times []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		times = append(times, time.Now())
		_, _ = w.Write(listingHTML(nil))
	}))
	defer srv.Close()

	cfg := globalcomix.DefaultConfig()
	cfg.BaseURL = srv.URL
	cfg.Rate = 50 * time.Millisecond
	cfg.Retries = 0
	c := globalcomix.NewClient(cfg)

	ctx := context.Background()
	_, _ = c.Trending(ctx, 1)
	_, _ = c.Trending(ctx, 2)

	if len(times) < 2 {
		t.Fatalf("expected at least 2 requests, got %d", len(times))
	}
	gap := times[1].Sub(times[0])
	if gap < 40*time.Millisecond {
		t.Errorf("requests too close: gap = %v, want >= 40ms", gap)
	}
}

func TestTrendingParsesComics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(listingHTML([]comicCard{
			{Slug: "batman-zero", Title: "Batman Zero"},
			{Slug: "spider-squad", Title: "Spider Squad"},
		}))
	}))
	defer srv.Close()

	cfg := globalcomix.DefaultConfig()
	cfg.BaseURL = srv.URL
	cfg.Rate = 0
	c := globalcomix.NewClient(cfg)

	comics, err := c.Trending(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(comics) < 2 {
		t.Fatalf("expected 2 comics, got %d", len(comics))
	}
	if comics[0].Slug != "batman-zero" {
		t.Errorf("Slug = %q, want batman-zero", comics[0].Slug)
	}
}

func TestGetComicDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(comicDetailHTML("test-comic", "Test Comic", "A great comic.", "https://example.com/cover.jpg"))
	}))
	defer srv.Close()

	cfg := globalcomix.DefaultConfig()
	cfg.BaseURL = srv.URL
	cfg.Rate = 0
	c := globalcomix.NewClient(cfg)

	detail, err := c.GetComic(context.Background(), "test-comic")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Slug != "test-comic" {
		t.Errorf("Slug = %q, want test-comic", detail.Slug)
	}
	if detail.Title != "Test Comic" {
		t.Errorf("Title = %q, want Test Comic", detail.Title)
	}
}

func TestComicRoundTrip(t *testing.T) {
	want := globalcomix.Comic{
		Slug:        "batman-zero",
		Title:       "Batman Zero",
		Description: "A hero in the dark",
		Cover:       "https://example.com/cover.jpg",
		Language:    "en",
		Genres:      "Action",
		Authors:     "Bob",
		URL:         "https://globalcomix.com/a/comics/batman-zero",
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got globalcomix.Comic
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Slug != want.Slug || got.Title != want.Title {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestChapterRoundTrip(t *testing.T) {
	want := globalcomix.Chapter{
		Slug:        "chapter-1",
		Title:       "The Beginning",
		Number:      "1",
		PublishedAt: "2024-01-01",
		URL:         "https://globalcomix.com/a/comics/test/chapter-1",
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got globalcomix.Chapter
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Slug != want.Slug || got.Number != want.Number {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestSearchParsesResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(listingHTML([]comicCard{
			{Slug: "batman-adventures", Title: "Batman Adventures"},
		}))
	}))
	defer srv.Close()

	cfg := globalcomix.DefaultConfig()
	cfg.BaseURL = srv.URL
	cfg.Rate = 0
	c := globalcomix.NewClient(cfg)

	comics, err := c.Search(context.Background(), "batman", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(comics) == 0 {
		t.Fatal("expected at least one result")
	}
	if comics[0].Slug != "batman-adventures" {
		t.Errorf("Slug = %q, want batman-adventures", comics[0].Slug)
	}
}

// ---- HTML fixtures ----

type comicCard struct {
	Slug  string
	Title string
}

func listingHTML(cards []comicCard) []byte {
	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html><html><head>`)
	sb.WriteString(`<meta property="og:title" content="GlobalComix - Top Comics"/>`)
	sb.WriteString(`</head><body>`)
	for _, c := range cards {
		sb.WriteString(`<div class="comic-card">`)
		sb.WriteString(`<a href="/a/comics/` + c.Slug + `" aria-label="` + c.Title + `">`)
		sb.WriteString(c.Title)
		sb.WriteString(`</a>`)
		sb.WriteString(`</div>`)
	}
	sb.WriteString(`</body></html>`)
	return []byte(sb.String())
}

func comicDetailHTML(slug, title, desc, cover string) []byte {
	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html><html><head>`)
	sb.WriteString(`<meta property="og:title" content="` + title + `"/>`)
	sb.WriteString(`<meta property="og:description" content="` + desc + `"/>`)
	sb.WriteString(`<meta property="og:image" content="` + cover + `"/>`)
	sb.WriteString(`<meta property="og:url" content="https://globalcomix.com/a/comics/` + slug + `"/>`)
	sb.WriteString(`</head><body>`)
	sb.WriteString(`<h1>` + title + `</h1>`)
	sb.WriteString(`</body></html>`)
	return []byte(sb.String())
}

