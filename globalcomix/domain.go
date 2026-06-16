package globalcomix

import (
	"context"

	"github.com/tamnd/any-cli/kit"
	"github.com/tamnd/any-cli/kit/errs"
)

// domain.go exposes globalcomix as a kit Domain.
//
// A multi-domain host enables this driver with:
//
//	import _ "github.com/tamnd/globalcomix-cli/globalcomix"
func init() { kit.Register(Domain{}) }

// Domain is the GlobalComix driver.
type Domain struct{}

// Info describes the scheme and identity.
func (Domain) Info() kit.DomainInfo {
	return kit.DomainInfo{
		Scheme:  "globalcomix",
		Aliases: []string{"gc"},
		Hosts:   []string{Host, "www.globalcomix.com"},
		Identity: kit.Identity{
			Binary: "gc",
			Short:  "Browse GlobalComix comics catalog",
			Long: `gc turns globalcomix.com into a fast, scriptable command line.

Browse trending and new comics, search the catalog, and fetch chapter lists
from GlobalComix's large free online library.

Quick start:
  gc trending -n 10               trending comics
  gc new -n 5                     newly added comics
  gc search "spider-man"          search by title
  gc comic one-piece-fan          comic details
  gc chapters one-piece-fan -n 5  chapter list`,
			Site: Host,
			Repo: "https://github.com/tamnd/globalcomix-cli",
		},
	}
}

// Register installs the client factory and all operations onto app.
func (Domain) Register(app *kit.App) {
	app.SetClient(newClient)

	kit.Handle(app, kit.OpMeta{
		Name:    "trending",
		Group:   "browse",
		List:    true,
		Summary: "List trending comics",
	}, trendingOp)

	kit.Handle(app, kit.OpMeta{
		Name:    "new",
		Group:   "browse",
		List:    true,
		Summary: "List newly added comics",
	}, newOp)

	kit.Handle(app, kit.OpMeta{
		Name:    "search",
		Group:   "browse",
		List:    true,
		Summary: "Search the GlobalComix catalog",
		Args: []kit.Arg{
			{Name: "query", Help: "search query"},
		},
	}, searchOp)

	kit.Handle(app, kit.OpMeta{
		Name:    "comic",
		Group:   "browse",
		Single:  true,
		Summary: "Fetch comic details by slug",
		Args: []kit.Arg{
			{Name: "slug", Help: "comic slug (from the URL)"},
		},
	}, comicOp)

	kit.Handle(app, kit.OpMeta{
		Name:    "chapters",
		Group:   "browse",
		List:    true,
		Summary: "List chapters for a comic",
		Args: []kit.Arg{
			{Name: "slug", Help: "comic slug"},
		},
	}, chaptersOp)
}

// newClient builds a *Client from the kit-resolved config.
func newClient(_ context.Context, cfg kit.Config) (any, error) {
	c := DefaultConfig()
	if cfg.UserAgent != "" {
		c.UserAgent = cfg.UserAgent
	}
	if cfg.Rate > 0 {
		c.Rate = cfg.Rate
	}
	if cfg.Retries > 0 {
		c.Retries = cfg.Retries
	}
	if cfg.Timeout > 0 {
		c.Timeout = cfg.Timeout
	}
	return NewClient(c), nil
}

// ---- input structs ----

type listingInput struct {
	Page   int     `kit:"flag" help:"page number (1-indexed)" default:"1"`
	Limit  int     `kit:"flag,inherit" help:"max results"`
	Client *Client `kit:"inject"`
}

type searchInput struct {
	Query  string  `kit:"arg"  help:"search query"`
	Page   int     `kit:"flag" help:"page number (1-indexed)" default:"1"`
	Limit  int     `kit:"flag,inherit" help:"max results"`
	Client *Client `kit:"inject"`
}

type comicInput struct {
	Slug   string  `kit:"arg"    help:"comic slug"`
	Client *Client `kit:"inject"`
}

type chaptersInput struct {
	Slug   string  `kit:"arg"    help:"comic slug"`
	Limit  int     `kit:"flag,inherit" help:"max chapters"`
	Client *Client `kit:"inject"`
}

// ---- handlers ----

func trendingOp(ctx context.Context, in listingInput, emit func(*Comic) error) error {
	comics, err := in.Client.Trending(ctx, in.Page)
	if err != nil {
		return mapErr(err)
	}
	if len(comics) == 0 {
		return errs.NotFound("no trending comics found")
	}
	return emitComics(comics, in.Limit, emit)
}

func newOp(ctx context.Context, in listingInput, emit func(*Comic) error) error {
	comics, err := in.Client.New(ctx, in.Page)
	if err != nil {
		return mapErr(err)
	}
	if len(comics) == 0 {
		return errs.NotFound("no new comics found")
	}
	return emitComics(comics, in.Limit, emit)
}

func searchOp(ctx context.Context, in searchInput, emit func(*Comic) error) error {
	comics, err := in.Client.Search(ctx, in.Query, in.Page)
	if err != nil {
		return mapErr(err)
	}
	if len(comics) == 0 {
		return errs.NotFound("no results for %q", in.Query)
	}
	return emitComics(comics, in.Limit, emit)
}

func comicOp(ctx context.Context, in comicInput, emit func(*ComicDetail) error) error {
	detail, err := in.Client.GetComic(ctx, in.Slug)
	if err != nil {
		return mapErr(err)
	}
	return emit(detail)
}

func chaptersOp(ctx context.Context, in chaptersInput, emit func(*Chapter) error) error {
	chapters, err := in.Client.GetChapters(ctx, in.Slug)
	if err != nil {
		return mapErr(err)
	}
	if len(chapters) == 0 {
		return errs.NotFound("no chapters found for %q", in.Slug)
	}
	limit := in.Limit
	if limit > 0 && limit < len(chapters) {
		chapters = chapters[:limit]
	}
	for i := range chapters {
		if err := emit(&chapters[i]); err != nil {
			return err
		}
	}
	return nil
}

func emitComics(comics []Comic, limit int, emit func(*Comic) error) error {
	if limit > 0 && limit < len(comics) {
		comics = comics[:limit]
	}
	for i := range comics {
		if err := emit(&comics[i]); err != nil {
			return err
		}
	}
	return nil
}

// mapErr converts library errors into kit error kinds.
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case err == ErrNotFound:
		return errs.NotFound("%s", err.Error())
	default:
		return err
	}
}
