package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/pocketbase/pocketbase"
	"github.com/spf13/cobra"

	"github.com/fastygo/market/internal/authoring"
	"github.com/fastygo/market/internal/cabinet"
	"github.com/fastygo/market/internal/editorial"
	"github.com/fastygo/market/internal/i18n"
	"github.com/fastygo/market/internal/platform/pb"
)

// registerCommands adds the operator commands (ADR-0035): role grants,
// dictionary import and the local demo seed.
func registerCommands(app *pocketbase.PocketBase) {
	role := &cobra.Command{Use: "role", Short: "Cabinet roles"}
	role.AddCommand(&cobra.Command{
		Use:   "grant <email> <editor|author|vendor|partner>",
		Short: "Grant a role to an existing user (editor is granted only here)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := &pb.Cabinet{App: app}
			if err := c.Grant(context.Background(), args[0], cabinet.Role(args[1]), ""); err != nil {
				return fmt.Errorf("grant: %w", err)
			}
			fmt.Println("granted", args[1])
			return nil
		},
	})
	app.RootCmd.AddCommand(role)

	tr := &cobra.Command{Use: "i18n", Short: "UI dictionaries"}
	tr.AddCommand(&cobra.Command{
		Use:   "import <locale> <file.json>",
		Short: "Import a DictionaryV04 file into ui_messages (MARKET_LOCALE selects it at start)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := os.ReadFile(args[1])
			if err != nil {
				return err
			}
			d, err := i18n.Parse(b)
			if err != nil {
				return err
			}
			n, err := (&pb.Editorial{App: app}).ImportMessages(context.Background(), args[0], d.Messages)
			if err != nil {
				return err
			}
			fmt.Println("imported", n, "messages for", args[0])
			return nil
		},
	})
	app.RootCmd.AddCommand(tr)

	demo := &cobra.Command{Use: "demo", Short: "Local demo data"}
	demo.AddCommand(&cobra.Command{
		Use:   "seed",
		Short: "Seed demo users for every role (local only: MARKET_DEMO=1, no public https URL)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := demoAllowed(os.Getenv); err != nil {
				return err
			}
			if err := app.RunAllMigrations(); err != nil {
				return err
			}
			return seedDemo(context.Background(), app, os.Getenv("MARKET_DEMO_PASSWORD"))
		},
	})
	app.RootCmd.AddCommand(demo)
}

// demoAllowed refuses the seed anywhere but a local development box.
func demoAllowed(getenv func(string) string) error {
	if getenv("MARKET_DEMO") != "1" {
		return errors.New("demo seed: set MARKET_DEMO=1 (local development only)")
	}
	for _, k := range []string{"MARKET_PUBLIC_URL", "MARKET_BASE_URL"} {
		u := getenv(k)
		if u != "" && !strings.HasPrefix(u, "http://127.0.0.1") && !strings.HasPrefix(u, "http://localhost") {
			return fmt.Errorf("demo seed: %s is not a local URL", k)
		}
	}
	if getenv("MARKET_YOOKASSA_SHOP_ID") != "" || getenv("MARKET_S3_ENDPOINT") != "" {
		return errors.New("demo seed: real providers are configured")
	}
	if len(getenv("MARKET_DEMO_PASSWORD")) < cabinet.MinPassword {
		return fmt.Errorf("demo seed: MARKET_DEMO_PASSWORD must have at least %d characters", cabinet.MinPassword)
	}
	return nil
}

// Demo accounts; the password comes from MARKET_DEMO_PASSWORD.
var demoUsers = []string{"editor@example.com", "author@example.com", "vendor@example.com", "partner@example.com", "buyer@example.com"}

func seedDemo(ctx context.Context, app *pocketbase.PocketBase, password string) error {
	users := &pb.Users{App: app}
	cab := &pb.Cabinet{App: app}
	ed := &pb.Editorial{App: app}
	items := &pb.Authoring{App: app}
	ids := map[string]string{}
	for _, e := range demoUsers {
		u, _, err := users.Ensure(ctx, e)
		if err != nil {
			return fmt.Errorf("user %s: %w", e, err)
		}
		_ = users.MarkVerified(ctx, u.ID)
		if err := cab.SetPassword(ctx, u.ID, password); err != nil {
			return fmt.Errorf("password %s: %w", e, err)
		}
		name := strings.SplitN(e, "@", 2)[0]
		_ = cab.SetName(ctx, u.ID, strings.ToUpper(name[:1])+name[1:]+" Demo")
		ids[e] = u.ID
	}
	if p, _ := cab.Principal(ctx, ids["editor@example.com"]); p.Editor {
		fmt.Println("demo seed: already seeded")
		return nil
	}
	editor := ids["editor@example.com"]
	if err := cab.Grant(ctx, "editor@example.com", cabinet.Editor, ""); err != nil {
		return err
	}
	// author, vendor and partner go through the application flow
	apps := []cabinet.Application{
		{UserID: ids["author@example.com"], Role: string(cabinet.Author), Message: "I write guides about UI kits."},
		{UserID: ids["vendor@example.com"], Role: string(cabinet.Vendor), StoreName: "Demo Studio", StoreSlug: "demo-studio",
			Website: "https://example.com", Message: "Templates and dashboards."},
		{UserID: ids["partner@example.com"], Role: string(cabinet.Partner), Message: "Newsletter about web design."},
		{UserID: ids["buyer@example.com"], Role: string(cabinet.Author), Message: "Pending application for the editor queue."},
	}
	for _, a := range apps {
		if err := cab.Apply(ctx, a); err != nil && !errors.Is(err, cabinet.ErrDuplicate) {
			return fmt.Errorf("apply %s: %w", a.Role, err)
		}
	}
	pending, err := cab.Applications(ctx, "pending")
	if err != nil {
		return err
	}
	for _, a := range pending {
		if a.UserID == ids["buyer@example.com"] {
			continue
		}
		if err := cab.Decide(ctx, a.ID, true, editor, "Welcome"); err != nil {
			return fmt.Errorf("decide %s: %w", a.Role, err)
		}
	}
	vp, err := cab.Principal(ctx, ids["vendor@example.com"])
	if err != nil || len(vp.Sellers) == 0 {
		return fmt.Errorf("vendor seller: %v", err)
	}
	sellerID := vp.Sellers[0].ID
	cats, _ := items.Categories(ctx)
	cat := ""
	if len(cats) > 0 {
		cat = cats[0].ID
	}
	_ = cab.SaveStore(ctx, sellerID, cabinet.StoreProfile{DisplayName: "Demo Studio", Headline: "Templates and dashboards for small teams",
		Bio: "A demo vendor created by market demo seed.", Website: "https://example.com"})
	products := []authoring.Meta{
		{Title: "Demo Landing Kit", Excerpt: "A landing page kit with twelve sections.", Category: cat, Stack: "HTML, Tailwind",
			Tags: "landing, tailwind", Price: 2900, Extended: 14900},
		{Title: "Demo Admin Dashboard", Excerpt: "An admin dashboard with charts and tables.", Category: cat, Stack: "Svelte",
			Tags: "dashboard, admin", Price: 4900, Extended: 19900},
	}
	for i, m := range products {
		it, err := items.Create(ctx, ids["vendor@example.com"], sellerID, m)
		if err != nil {
			return fmt.Errorf("product: %w", err)
		}
		if _, err := items.SaveDescription(ctx, ids["vendor@example.com"], it.ID,
			"## What is inside\n\n- Responsive sections\n- Light and dark themes\n- Documentation\n", it.Version); err != nil {
			return fmt.Errorf("description: %w", err)
		}
		if _, err := items.Act(ctx, ids["vendor@example.com"], it.ID, authoring.ActSubmit); err != nil {
			return fmt.Errorf("submit: %w", err)
		}
		if i == 0 {
			if err := ed.ModerateProduct(ctx, it.ID, "published", ""); err != nil {
				return err
			}
		}
	}
	author := ids["author@example.com"]
	arts := []editorial.Article{
		{Title: "How to choose a UI kit", Kind: "guide", Excerpt: "Five questions to ask before you buy a template.",
			Body: "## Start with the stack\n\nPick a kit that matches the framework your team already uses.\n\n## Check the license\n\nRegular covers one end product; extended covers products you sell.\n"},
		{Title: "What is new in the marketplace", Kind: "news", Excerpt: "Collections, reviews and partner links.",
			Body: "The marketplace now has curated collections, buyer reviews and a partner program.\n"},
	}
	for i, a := range arts {
		saved, err := ed.SaveArticle(ctx, a, author, true)
		if err != nil {
			return fmt.Errorf("article: %w", err)
		}
		if i == 0 {
			if err := ed.ModerateArticle(ctx, saved.ID, "published", ""); err != nil {
				return err
			}
		}
	}
	var slugs []string
	if recs, err := app.FindRecordsByFilter("products", "status = 'published'", "-created", 4, 0); err == nil {
		for _, r := range recs {
			slugs = append(slugs, r.GetString("slug"))
		}
	}
	if err := ed.SaveCollection(ctx, editorial.Collection{Slug: "starter-picks", Title: "Starter picks", Eyebrow: "Collection",
		Lead: "Kits that get a project going in a day.", Cta: "Open collection", Tone: "accent", Status: "published", ProductIDs: slugs}); err != nil {
		return fmt.Errorf("collection: %w", err)
	}
	out, _ := json.Marshal(demoUsers)
	fmt.Println("demo seed: users", string(out), "(password from MARKET_DEMO_PASSWORD)")
	return nil
}
