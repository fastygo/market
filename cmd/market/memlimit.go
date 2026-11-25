package main

import (
	"os"
	"strconv"
	"strings"
)

// defaultMemLimit is the slo.md budget for the binary (Go heap + SQLite
// page cache) on the 2 GB host.
const defaultMemLimit = 512 << 20

// memLimitRatio of the container memory limit (incus limits.memory,
// systemd MemoryMax) leaves headroom for non-heap memory (stacks, SQLite
// mmap, fragmentation) so the GC works harder before the OOM killer acts.
const memLimitRatio = 0.85

// memLimit is the soft memory limit used when GOMEMLIMIT is not set (the
// Go runtime applies GOMEMLIMIT itself): the smaller of the slo.md budget
// and 85% of the cgroup memory limit, if there is one.
func memLimit(read func(string) ([]byte, error)) (limit int64, source string) {
	limit, source = defaultMemLimit, "default 512MiB"
	for _, f := range []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"} {
		b, err := read(f)
		if err != nil {
			continue
		}
		v := strings.TrimSpace(string(b))
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 || n >= 1<<60 { // "max" or the v1 "unlimited" sentinel
			break
		}
		if c := int64(float64(n) * memLimitRatio); c < limit {
			limit, source = c, "85% of cgroup "+f
		}
		break
	}
	return limit, source
}

var readFile = os.ReadFile
