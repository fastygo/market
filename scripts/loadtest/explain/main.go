// Command explain prints EXPLAIN QUERY PLAN and the run time of the main
// storefront queries on a (seeded copy of a) data.db, read-only unless
// -analyze.
//
//	go run ./scripts/loadtest/explain -db /tmp/market-loadtest/seed/data.db
package main

import (
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	_ "modernc.org/sqlite"
)

func main() {
	path := flag.String("db", "", "data.db path (opened read-only)")
	analyze := flag.Bool("analyze", false, "run ANALYZE first (opens read-write: use a copy)")
	shape := flag.String("shape", "v2", "query shape: v1 (before 2026-11-25: implicit join order, slug filters on joined tables) or v2")
	flag.Parse()
	mode := "ro"
	if *analyze {
		mode = "rw"
	}
	db, err := dbx.Open("sqlite", "file:"+*path+"?mode="+mode+"&_pragma=busy_timeout(5000)")
	if err != nil {
		log.Fatal(err)
	}
	if *analyze {
		if _, err := db.NewQuery("ANALYZE").Execute(); err != nil {
			log.Fatal(err)
		}
		fmt.Println("(after ANALYZE)")
	}
	var seller, product, cat string
	_ = db.NewQuery(`SELECT s.slug, p.slug FROM products p JOIN sellers s ON s.id = p.seller WHERE p.status='published' LIMIT 1 OFFSET 500`).Row(&seller, &product)
	_ = db.NewQuery(`SELECT slug FROM categories ORDER BY sort DESC LIMIT 1`).Row(&cat)
	sel := `SELECT p.id, p.slug, p.title, p.excerpt, p.description, p.product_type, p.demo_url, p.cover,
       c.slug AS cat_slug, c.title AS cat_title, s.display_name AS seller_name, s.slug AS seller_slug,
       (SELECT MIN(o.price_minor) FROM offers o WHERE o.product = p.id AND o.active = TRUE AND o.currency = 'RUB') AS price_minor
FROM products p CROSS JOIN sellers s ON s.id = p.seller LEFT JOIN categories c ON c.id = p.category
WHERE p.status = 'published'`
	catF := ` AND p.category = (SELECT id FROM categories WHERE slug = '` + cat + `')`
	sellerF := ` AND p.seller = (SELECT id FROM sellers WHERE slug = '` + seller + `')`
	if *shape == "v1" {
		sel = strings.Replace(sel, "CROSS JOIN", "JOIN", 1)
		catF = ` AND c.slug = '` + cat + `'`
		sellerF = ` AND s.slug = '` + seller + `'`
	}
	order := ` ORDER BY p.published_at DESC, p.created DESC`
	searchF := ` AND p.id IN (SELECT id FROM products_fts WHERE products_fts MATCH '"дизайн"*' UNION SELECT id FROM products_fts_tri WHERE products_fts_tri MATCH '"дизайн"')`
	if *shape == "v1" {
		searchF = ` AND (p.title LIKE '%дизайн%' OR p.excerpt LIKE '%дизайн%' OR s.display_name LIKE '%дизайн%' OR c.title LIKE '%дизайн%')`
	}
	for _, q := range []struct{ name, sql string }{
		{"home/catalog: newest 100", sel + order + ` LIMIT 100`},
		{"catalog page 50 (OFFSET 4900)", sel + order + ` LIMIT 100 OFFSET 4900`},
		{"category", sel + catF + order + ` LIMIT 100`},
		{"author items", sel + sellerF + order + ` LIMIT 100`},
		{"product by slug", sel + ` AND p.slug = '` + product + `' LIMIT 1`},
		{"seller profile + count", `SELECT s.id, (SELECT COUNT(*) FROM products p WHERE p.seller = s.id AND p.status = 'published') AS items FROM sellers s WHERE s.slug = '` + seller + `' AND s.status = 'active' LIMIT 1`},
		{"search 'дизайн' (v1 LIKE, v2 FTS5)", sel + searchF + order + ` LIMIT 100`},
	} {
		fmt.Printf("### %s\n```\n", q.name)
		var plan []struct {
			ID     int    `db:"id"`
			Parent int    `db:"parent"`
			NotUse int    `db:"notused"`
			Detail string `db:"detail"`
		}
		if err := db.NewQuery("EXPLAIN QUERY PLAN " + q.sql).All(&plan); err != nil {
			log.Fatalf("%s: %v", q.name, err)
		}
		depth := map[int]int{}
		for _, p := range plan {
			depth[p.ID] = depth[p.Parent] + 1
			fmt.Printf("%s%s\n", strings.Repeat("  ", depth[p.ID]-1), p.Detail)
		}
		var rows []dbx.NullStringMap
		start := time.Now()
		const n = 5
		for i := 0; i < n; i++ {
			rows = nil
			if err := db.NewQuery(q.sql).All(&rows); err != nil {
				log.Fatal(err)
			}
		}
		fmt.Printf("```\n%d rows, %.2f ms per run\n\n", len(rows), float64(time.Since(start).Microseconds())/1000/n)
	}
}
