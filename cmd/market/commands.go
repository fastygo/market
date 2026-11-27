package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pocketbase/pocketbase"
	"github.com/spf13/cobra"

	"github.com/fastygo/market/internal/authoring"
	"github.com/fastygo/market/internal/cabinet"
	"github.com/fastygo/market/internal/editorial"
	"github.com/fastygo/market/internal/i18n"
	"github.com/fastygo/market/internal/platform/pb"
	"github.com/fastygo/market/ui/dict"
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

	seller := &cobra.Command{Use: "seller", Short: "Stores"}
	seller.AddCommand(&cobra.Command{
		Use:   "vat <store-slug> <yes|no>",
		Short: "Record whether a store pays VAT (required before its items go on sale)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var payer bool
			switch args[1] {
			case "yes":
				payer = true
			case "no":
			default:
				return errors.New("seller vat: answer yes or no")
			}
			if err := (&pb.Cabinet{App: app}).SetSellerVAT(context.Background(), args[0], payer); err != nil {
				return fmt.Errorf("seller vat: %w", err)
			}
			fmt.Println("vat_payer", args[1], "for", args[0])
			return nil
		},
	})
	app.RootCmd.AddCommand(seller)

	tr := &cobra.Command{Use: "i18n", Short: "UI dictionaries"}
	tr.AddCommand(&cobra.Command{
		Use:   "check <file.json>",
		Short: "Report keys a DictionaryV04 file misses against the built-in English source",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			d, err := i18n.Parse(b)
			if err != nil {
				return err
			}
			return reportCoverage(d)
		},
	})
	var force bool
	imp := &cobra.Command{
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
			if err := reportCoverage(d); err != nil && !force {
				return fmt.Errorf("%w (use --force to import anyway)", err)
			}
			n, err := (&pb.Editorial{App: app}).ImportMessages(context.Background(), args[0], d.Messages)
			if err != nil {
				return err
			}
			fmt.Println("imported", n, "messages for", args[0])
			return nil
		},
	}
	imp.Flags().BoolVar(&force, "force", false, "import even when keys are missing")
	tr.AddCommand(imp)
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
	if err := cab.SetSellerVAT(ctx, "demo-studio", false); err != nil {
		return fmt.Errorf("demo vat: %w", err)
	}
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
		if err := seedRelease(ctx, app, ids["vendor@example.com"], sellerID, it.ID, it.Slug); err != nil {
			return fmt.Errorf("release: %w", err)
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

// reportCoverage prints the gaps of d against the English source and fails
// when any key is missing, unknown or has different placeholders.
func reportCoverage(d *i18n.Dict) error {
	src, err := i18n.Parse(dict.EN)
	if err != nil {
		return err
	}
	r := src.Check(d.Messages)
	for _, k := range r.Missing {
		fmt.Println("missing", k)
	}
	for _, k := range r.Extra {
		fmt.Println("unknown", k)
	}
	for _, k := range r.Placeholders {
		fmt.Println("placeholders differ", k)
	}
	fmt.Printf("%s: %d of %d keys translated\n", d.Locale, len(src.Messages)-len(r.Missing), len(src.Messages))
	if !r.OK() {
		return errors.New("i18n: dictionary does not match the source")
	}
	return nil
}

// seedRelease writes a tiny demo archive into the local files directory
// and publishes it as release 1.0.0 (demo seed only).
func seedRelease(ctx context.Context, app *pocketbase.PocketBase, userID, sellerID, itemID, slug string) error {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create("README.md")
	if err != nil {
		return err
	}
	if _, err := f.Write([]byte("# " + slug + "\n\nDemo release created by market demo seed.\n")); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	dir := os.Getenv("MARKET_FILES_DIR")
	if dir == "" {
		dir = filepath.Join(app.DataDir(), "files")
	}
	sum := sha256.Sum256(buf.Bytes())
	hexSum := hex.EncodeToString(sum[:])
	key := sellerID + "/" + itemID + "/" + hexSum[:16] + "/" + slug + "-1.0.0.zip"
	full := filepath.Join(dir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(full, buf.Bytes(), 0o640); err != nil {
		return err
	}
	rels := &pb.Releases{App: app}
	rl, err := rels.AddRelease(ctx, userID, itemID, authoring.NewRelease{Version: "1.0.0", Changelog: "First release.",
		StorageKey: key, SHA256: hexSum, Size: int64(buf.Len())})
	if err != nil {
		return err
	}
	return rels.PublishRelease(ctx, userID, itemID, rl.ID)
}
