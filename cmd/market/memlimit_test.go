package main

import (
	"errors"
	"testing"
)

func TestMemLimit(t *testing.T) {
	files := func(m map[string]string) func(string) ([]byte, error) {
		return func(p string) ([]byte, error) {
			if v, ok := m[p]; ok {
				return []byte(v), nil
			}
			return nil, errors.New("missing")
		}
	}
	for name, c := range map[string]struct {
		files map[string]string
		want  int64
	}{
		"no cgroup":         {nil, 512 << 20},
		"v2 unlimited":      {map[string]string{"/sys/fs/cgroup/memory.max": "max\n"}, 512 << 20},
		"v2 512MiB":         {map[string]string{"/sys/fs/cgroup/memory.max": "536870912\n"}, 456340275},
		"v2 2GiB keeps 512": {map[string]string{"/sys/fs/cgroup/memory.max": "2147483648"}, 512 << 20},
		"v1 384MiB":         {map[string]string{"/sys/fs/cgroup/memory/memory.limit_in_bytes": "402653184"}, 342255206},
		"v1 unlimited":      {map[string]string{"/sys/fs/cgroup/memory/memory.limit_in_bytes": "9223372036854771712"}, 512 << 20},
	} {
		if got, _ := memLimit(files(c.files)); got != c.want {
			t.Errorf("%s: memLimit = %d, want %d", name, got, c.want)
		}
	}
}
