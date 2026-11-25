// Command run drives the load test against a local market binary started
// on a seeded data dir (see scripts/loadtest/README.md). Each scenario is
// a closed loop of -c workers for -d; the report has client-side
// p50/p95/p99, RPS and errors per scenario plus server-side numbers from
// the loopback /metrics: statements per request, DB and render time,
// RSS, heap and goroutines before and after.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	mrand "math/rand/v2"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	base    = flag.String("base", "http://127.0.0.1:8090", "storefront origin")
	metrics = flag.String("metrics", "http://127.0.0.1:9091/metrics", "loopback metrics URL")
	data    = flag.String("data", "", "seeded pb_data dir (lt-*.txt lists)")
	conc    = flag.Int("c", 16, "concurrent workers per scenario")
	dur     = flag.Duration("d", 20*time.Second, "duration per scenario")
	only    = flag.String("only", "", "comma separated scenario names (default all)")
	label   = flag.String("label", "", "label for the report header")
)

func lines(name string) []string {
	b, err := os.ReadFile(filepath.Join(*data, name))
	if err != nil {
		log.Fatal(err)
	}
	return strings.Fields(string(b))
}

type result struct {
	name   string
	lat    []time.Duration
	errs   int
	status map[int]int
	took   time.Duration
	srv    map[string]float64 // server-side deltas
}

func pct(s []time.Duration, p float64) time.Duration {
	if len(s) == 0 {
		return 0
	}
	i := int(float64(len(s)-1) * p)
	return s[i]
}

var client = &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 256},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// scenario: setup per worker returns the request func for one iteration
type scenario struct {
	name  string
	route string // metrics route label
	setup func(worker int) func(r *mrand.Rand) (int, error)
}

func get(c *http.Client, u string) (int, error) {
	res, err := c.Get(u)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res.StatusCode, nil
}

var inputRe = regexp.MustCompile(`<input[^>]*>`)
var attrRe = regexp.MustCompile(`(name|value)="([^"]*)"`)

func hidden(html string) map[string]string {
	out := map[string]string{}
	for _, tag := range inputRe.FindAllString(html, -1) {
		var n, v string
		for _, m := range attrRe.FindAllStringSubmatch(tag, -1) {
			if m[1] == "name" {
				n = m[2]
			} else {
				v = m[2]
			}
		}
		if n != "" {
			out[n] = v
		}
	}
	return out
}

func fetch(c *http.Client, u string) (string, error) {
	res, err := c.Get(u)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		return "", fmt.Errorf("GET %s: %d", u, res.StatusCode)
	}
	return string(b), err
}

func login(email, password string) *http.Client {
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Timeout: 30 * time.Second, Jar: jar, Transport: client.Transport, CheckRedirect: client.CheckRedirect}
	page, err := fetch(c, *base+"/account")
	if err != nil {
		log.Fatal(err)
	}
	f := hidden(page)
	res, err := c.PostForm(*base+"/account/login", url.Values{"csrf": {f["csrf"]}, "email": {email}, "password": {password}, "next": {"/"}})
	if err != nil {
		log.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		log.Fatalf("login %s: %d", email, res.StatusCode)
	}
	return c
}

func scrape() map[string]float64 {
	out := map[string]float64{}
	res, err := http.Get(*metrics)
	if err != nil {
		log.Printf("metrics: %v", err)
		return out
	}
	defer res.Body.Close()
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(l, "#") {
			continue
		}
		i := strings.LastIndexByte(l, ' ')
		if i < 0 {
			continue
		}
		v, err := strconv.ParseFloat(l[i+1:], 64)
		if err == nil {
			out[l[:i]] = v
		}
	}
	return out
}

func run(s scenario) result {
	before := scrape()
	ctx, cancel := context.WithTimeout(context.Background(), *dur)
	defer cancel()
	var mu sync.Mutex
	res := result{name: s.name, status: map[int]int{}}
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < *conc; w++ {
		do := s.setup(w)
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			r := mrand.New(mrand.NewPCG(uint64(w), 99))
			var lat []time.Duration
			st := map[int]int{}
			errs := 0
			for ctx.Err() == nil {
				t := time.Now()
				code, err := do(r)
				d := time.Since(t)
				if err != nil || code >= 500 || code == 0 {
					errs++
				}
				st[code]++
				lat = append(lat, d)
			}
			mu.Lock()
			res.lat = append(res.lat, lat...)
			res.errs += errs
			for k, v := range st {
				res.status[k] += v
			}
			mu.Unlock()
		}(w)
	}
	wg.Wait()
	res.took = time.Since(start)
	sort.Slice(res.lat, func(i, j int) bool { return res.lat[i] < res.lat[j] })
	after := scrape()
	res.srv = map[string]float64{}
	if s.route != "" {
		key := func(m, suffix string) string { return m + suffix + `{route="` + s.route + `"}` }
		for _, m := range []string{"market_http_request_db_queries", "market_http_request_db_seconds", "market_ssr_render_seconds"} {
			n := after[key(m, "_count")] - before[key(m, "_count")]
			if n > 0 {
				res.srv[m] = (after[key(m, "_sum")] - before[key(m, "_sum")]) / n
			}
		}
	}
	return res
}

func main() {
	flag.Parse()
	products, free, sellers := lines("lt-products.txt"), lines("lt-free.txt"), lines("lt-sellers.txt")
	users, password := lines("lt-users.txt"), lines("lt-password.txt")[0]
	cats := []string{"ui-kits", "dashboards", "design-systems", "icons", "illustrations", "landing-pages"}
	words := []string{"dashboard", "дизайн", "кабинет", "wallet", "ash", "UI kit", "иконок", "Fintech"}
	lastPage := (len(products) + 99) / 100
	plain := func(route string, u func(r *mrand.Rand) string) func(int) func(*mrand.Rand) (int, error) {
		return func(int) func(*mrand.Rand) (int, error) {
			return func(r *mrand.Rand) (int, error) { return get(client, *base+u(r)) }
		}
	}
	scenarios := []scenario{
		{"home", "GET /{$}", plain("", func(*mrand.Rand) string { return "/" })},
		{"catalog p1", "GET /catalog", plain("", func(*mrand.Rand) string { return "/catalog" })},
		{"catalog p50", "GET /catalog", plain("", func(*mrand.Rand) string { return "/catalog?page=50" })},
		{"catalog last", "GET /catalog", plain("", func(*mrand.Rand) string { return "/catalog?page=" + strconv.Itoa(lastPage) })},
		{"category", "GET /catalog/{slug}", plain("", func(r *mrand.Rand) string { return "/catalog/" + cats[r.IntN(len(cats))] })},
		{"product", "GET /products/{slug}", plain("", func(r *mrand.Rand) string { return "/products/" + products[r.IntN(len(products))] })},
		{"author", "GET /authors/{slug}", plain("", func(r *mrand.Rand) string { return "/authors/" + sellers[r.IntN(len(sellers))] })},
		{"search", "GET /catalog", plain("", func(r *mrand.Rand) string {
			return "/catalog?q=" + url.QueryEscape(words[r.IntN(len(words))])
		})},
		{"checkout free", "POST /checkout/form", func(w int) func(*mrand.Rand) (int, error) {
			c := login(users[w%len(users)], password)
			return func(r *mrand.Rand) (int, error) {
				slug := free[r.IntN(len(free))]
				page, err := fetch(c, *base+"/products/"+slug)
				if err != nil {
					return 0, err
				}
				f := hidden(page)
				res, err := c.PostForm(*base+"/checkout/form", url.Values{"csrf": {f["csrf"]}, "key": {f["key"]},
					"product": {slug}, "terms": {"1"}})
				if err != nil {
					return 0, err
				}
				_, _ = io.Copy(io.Discard, res.Body)
				res.Body.Close()
				if res.StatusCode != http.StatusSeeOther {
					return res.StatusCode, fmt.Errorf("checkout %d", res.StatusCode)
				}
				return res.StatusCode, nil
			}
		}},
	}
	want := map[string]bool{}
	for _, n := range strings.Split(*only, ",") {
		if n != "" {
			want[n] = true
		}
	}
	m0 := scrape()
	fmt.Printf("## %s\n\nc=%d, d=%s per scenario, %d published products, %d sellers\n\n", *label, *conc, *dur, len(products), len(sellers))
	fmt.Println("| scenario | requests | RPS | p50 ms | p95 ms | p99 ms | errors | statuses | stmts/req | db ms/req | render ms/req |")
	fmt.Println("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |")
	for _, s := range scenarios {
		if len(want) > 0 && !want[s.name] {
			continue
		}
		r := run(s)
		ms := func(d time.Duration) string { return fmt.Sprintf("%.2f", float64(d.Microseconds())/1000) }
		var st []string
		for k, v := range r.status {
			st = append(st, fmt.Sprintf("%d:%d", k, v))
		}
		sort.Strings(st)
		fmt.Printf("| %s | %d | %.0f | %s | %s | %s | %d | %s | %.1f | %.2f | %.2f |\n", r.name, len(r.lat),
			float64(len(r.lat))/r.took.Seconds(), ms(pct(r.lat, .5)), ms(pct(r.lat, .95)), ms(pct(r.lat, .99)), r.errs,
			strings.Join(st, " "), r.srv["market_http_request_db_queries"], r.srv["market_http_request_db_seconds"]*1000,
			r.srv["market_ssr_render_seconds"]*1000)
	}
	time.Sleep(2 * time.Second)
	m1 := scrape()
	fmt.Println()
	fmt.Println("| process | before | after |")
	fmt.Println("| --- | --- | --- |")
	for _, k := range []string{"process_resident_memory_bytes", "go_memstats_heap_inuse_bytes", "go_goroutines",
		`go_sql_wait_duration_seconds_total{db_name="nonconcurrent"}`, `go_sql_wait_count_total{db_name="nonconcurrent"}`} {
		f := func(v float64) string {
			if strings.HasSuffix(k, "_bytes") {
				return fmt.Sprintf("%.1f MiB", v/(1<<20))
			}
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
		fmt.Printf("| %s | %s | %s |\n", k, f(m0[k]), f(m1[k]))
	}
	for k, v := range m1 {
		if strings.HasPrefix(k, "market_tx_errors_total") || strings.HasPrefix(k, "market_tx_busy_retries_total") ||
			strings.HasPrefix(k, "market_db_statement_errors_total") {
			fmt.Printf("| %s | %v | %v |\n", k, m0[k], v)
		}
	}
}
