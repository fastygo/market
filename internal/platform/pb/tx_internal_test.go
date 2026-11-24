package pb

import "testing"

func TestIsBusy(t *testing.T) {
	if !isBusy(errFake("database is locked (5) (SQLITE_BUSY)")) || isBusy(errFake("x")) || isBusy(nil) {
		t.Fatal("isBusy")
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }
