package pb_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/fastygo/market/internal/platform/pb"
	"github.com/fastygo/market/internal/upload"
)

func TestReleaseQuotaInTx(t *testing.T) {
	app := newApp(t)
	seller, err := app.FindFirstRecordByData("sellers", "slug", "godudy")
	if err != nil {
		t.Fatal(err)
	}
	prod, err := app.FindFirstRecordByData("products", "slug", "aurora-theme")
	if err != nil {
		t.Fatal(err)
	}
	var used struct {
		N    int   `db:"n"`
		Size int64 `db:"size"`
	}
	if err := app.DB().NewQuery("SELECT COUNT(*) AS n, COALESCE(SUM(size), 0) AS size FROM product_releases WHERE seller = {:s}").
		Bind(map[string]any{"s": seller.Id}).One(&used); err != nil {
		t.Fatal(err)
	}
	rel := &pb.Releases{App: app}
	q := upload.Quota{MaxFiles: used.N + 3, MaxBytes: used.Size + 10<<20}

	// 8 parallel uploads of 1 MiB race for 3 free slots: exactly 3 win
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, quota := 0, 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := rel.Create(context.Background(), pb.NewRelease{SellerID: seller.Id, ProductID: prod.Id,
				Version: fmt.Sprintf("2.0.%d", i), StorageKey: fmt.Sprintf("k%d", i), Size: 1 << 20}, q)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, upload.ErrQuota):
				quota++
			default:
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if ok != 3 || quota != 5 {
		t.Fatalf("created %d, over quota %d; want 3 and 5", ok, quota)
	}
	// byte quota
	q = upload.Quota{MaxFiles: 1000, MaxBytes: used.Size + 3<<20 + 1}
	if _, err := rel.Create(context.Background(), pb.NewRelease{SellerID: seller.Id, ProductID: prod.Id,
		Version: "3.0.0", Size: 2 << 20}, q); !errors.Is(err, upload.ErrQuota) {
		t.Fatalf("byte quota: %v", err)
	}
}
