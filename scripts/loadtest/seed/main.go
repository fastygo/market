// Command seed fills a throw-away pb_data with a synthetic catalog for the
// load test (slo.md "Load test plan"): N products, M active sellers,
// realistic categories, one regular offer per product (about 10% free),
// published releases, and buyer accounts. Never point it at a real data
// dir: it refuses a dir that already has a database.
//
//	go run ./scripts/loadtest/seed -dir /tmp/lt/pb_data -products 10000 -sellers 500
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	mrand "math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"

	_ "github.com/fastygo/market/internal/migrations"
	"github.com/fastygo/market/internal/platform/pb"
)

var categories = []struct{ slug, title string }{
	{"ui-kits", "UI kits"}, {"dashboards", "Dashboards"}, {"design-systems", "Design systems"},
	{"icons", "Icons"}, {"illustrations", "Illustrations"}, {"landing-pages", "Landing pages"},
	{"mobile-apps", "Mobile apps"}, {"wireframes", "Wireframes"}, {"mockups", "Mockups"},
	{"fonts", "Fonts"}, {"presentations", "Presentations"}, {"email-templates", "Email templates"},
}

var (
	adjectives = []string{"Minimal", "Bold", "Clean", "Modern", "Dark", "Pastel", "Glass", "Neo", "Retro", "Soft",
		"Тёмный", "Светлый", "Строгий", "Яркий", "Лёгкий", "Деловой"}
	nouns = []string{"Dashboard", "Admin", "CRM", "Wallet", "Shop", "Blog", "Portfolio", "SaaS", "Fintech", "Booking",
		"Дашборд", "Магазин", "Портфолио", "Лендинг", "Кабинет", "Аналитика"}
	kinds = []string{"UI kit", "Figma template", "design system", "icon set", "starter", "wireframe kit",
		"дизайн система", "набор иконок", "шаблон"}
	first = []string{"Alex", "Maria", "Ivan", "Olga", "Pixel", "North", "Studio", "Nova", "Artem", "Daria", "Мастерская", "Студия"}
	last  = []string{"Design", "Lab", "Works", "Craft", "Forge", "Petrov", "Smirnova", "Kit", "UI", "Дизайн", "Форма"}
)

func pick(r *mrand.Rand, s []string) string { return s[r.IntN(len(s))] }

func main() {
	dir := flag.String("dir", "", "pb_data dir to create (must not contain data.db)")
	nProducts := flag.Int("products", 10000, "products")
	nSellers := flag.Int("sellers", 500, "active sellers")
	nUsers := flag.Int("users", 50, "buyer accounts (password in users.txt)")
	flag.Parse()
	if *dir == "" {
		log.Fatal("-dir is required")
	}
	if _, err := os.Stat(filepath.Join(*dir, "data.db")); err == nil {
		log.Fatalf("%s already has data.db; the seed only writes into a fresh dir", *dir)
	}
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: *dir, DBConnect: pb.DBConnect})
	if err := app.Bootstrap(); err != nil {
		log.Fatal(err)
	}
	if err := app.RunAllMigrations(); err != nil {
		log.Fatal(err)
	}
	r := mrand.New(mrand.NewPCG(42, 7))
	start := time.Now()
	var slugs, free, sellerSlugs []string
	err := app.RunInTransaction(func(tx core.App) error {
		col := func(name string) *core.Collection {
			c, err := tx.FindCollectionByNameOrId(name)
			if err != nil {
				log.Fatal(err)
			}
			return c
		}
		catCol, selCol, prodCol, offCol, relCol := col("categories"), col("sellers"), col("products"), col("offers"), col("product_releases")
		var catIDs []string
		for i, c := range categories {
			rec := core.NewRecord(catCol)
			rec.Load(map[string]any{"slug": c.slug, "title": c.title, "sort": 100 + i})
			if err := tx.Save(rec); err != nil {
				return err
			}
			catIDs = append(catIDs, rec.Id)
		}
		var sellerIDs []string
		for i := 0; i < *nSellers; i++ {
			rec := core.NewRecord(selCol)
			slug := fmt.Sprintf("lt-seller-%d", i)
			rec.Load(map[string]any{"slug": slug, "display_name": pick(r, first) + " " + pick(r, last) + fmt.Sprintf(" %d", i),
				"status": "active", "headline": "Digital goods for designers", "bio": strings.Repeat("Bio paragraph. ", 20),
				"legal_ids": map[string]any{}, "tax_profile": map[string]any{"vat_payer": false}, "sale_model": "principal"})
			if err := tx.Save(rec); err != nil {
				return err
			}
			sellerIDs = append(sellerIDs, rec.Id)
			sellerSlugs = append(sellerSlugs, slug)
		}
		desc := strings.Repeat("A complete set of screens, components and styles with auto layout and variables. ", 12)
		for i := 0; i < *nProducts; i++ {
			title := pick(r, adjectives) + " " + pick(r, nouns) + " " + pick(r, kinds)
			slug := fmt.Sprintf("lt-%d", i)
			status := "published"
			if r.IntN(20) == 0 {
				status = "draft"
			}
			seller := sellerIDs[r.IntN(len(sellerIDs))]
			pr := core.NewRecord(prodCol)
			pr.Load(map[string]any{"seller": seller, "slug": slug, "product_type": "plain",
				"category": catIDs[r.IntN(len(catIDs))], "title": title, "excerpt": title + " for teams and freelancers.",
				"description": desc, "status": status, "demo_url": "https://example.com/demo/" + slug,
				"published_at": time.Now().Add(-time.Duration(r.IntN(730*24)) * time.Hour).UTC().Format("2006-01-02 15:04:05.000Z")})
			if err := tx.Save(pr); err != nil {
				return err
			}
			price := int64(0)
			if r.IntN(10) != 0 {
				price = int64(490+r.IntN(150)*100) * 100
			}
			of := core.NewRecord(offCol)
			of.Load(map[string]any{"product": pr.Id, "seller": seller, "license_type": "regular", "price_minor": price,
				"currency": "RUB", "tax_category": "standard", "support_months": 6, "active": true})
			if err := tx.Save(of); err != nil {
				return err
			}
			rl := core.NewRecord(relCol)
			rl.Load(map[string]any{"product": pr.Id, "seller": seller, "version": "1.0.0", "status": "published",
				"storage_key": "loadtest/" + slug + ".zip", "size": 1 << 20, "released_at": "2026-11-24 00:00:00.000Z"})
			if err := tx.Save(rl); err != nil {
				return err
			}
			if status == "published" {
				slugs = append(slugs, slug)
				if price == 0 {
					free = append(free, slug)
				}
			}
		}
		// spread `created` like a real catalog grown over time
		_, err := tx.DB().NewQuery(`UPDATE products SET created = published_at WHERE slug LIKE 'lt-%'`).Execute()
		return err
	})
	if err != nil {
		log.Fatal(err)
	}
	userCol, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		log.Fatal(err)
	}
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	password := hex.EncodeToString(b) // throw-away, for this temp dir only
	var users []string
	for i := 0; i < *nUsers; i++ {
		u := core.NewRecord(userCol)
		email := fmt.Sprintf("lt-buyer-%d@example.com", i)
		u.SetEmail(email)
		u.SetPassword(password)
		u.SetVerified(true)
		if err := app.Save(u); err != nil {
			log.Fatal(err)
		}
		users = append(users, email)
	}
	write := func(name string, lines []string) {
		f, err := os.Create(filepath.Join(*dir, name))
		if err != nil {
			log.Fatal(err)
		}
		w := bufio.NewWriter(f)
		for _, l := range lines {
			fmt.Fprintln(w, l)
		}
		_ = w.Flush()
		_ = f.Close()
	}
	write("lt-products.txt", slugs)
	write("lt-free.txt", free)
	write("lt-sellers.txt", sellerSlugs)
	write("lt-users.txt", users)
	write("lt-password.txt", []string{password})
	if _, err := app.DB().NewQuery("PRAGMA wal_checkpoint(TRUNCATE)").Execute(); err != nil {
		log.Print(err)
	}
	_ = app.ResetBootstrapState()
	log.Printf("seeded %d products (%d published, %d free), %d sellers, %d users in %s",
		*nProducts, len(slugs), len(free), *nSellers, *nUsers, time.Since(start).Round(time.Millisecond))
}
