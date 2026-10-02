package job

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var sizeRe = regexp.MustCompile(`^([0-9]+)\s*([KMGT]i?B?|B)?$`)

// ParseSize parses "32GiB", "32G" (binary) or "32GB" (decimal) into bytes.
func ParseSize(s string) (int64, error) {
	m := sizeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	unit := m[2]
	var mult int64 = 1
	switch strings.TrimSuffix(strings.TrimSuffix(unit, "B"), "i") {
	case "":
		mult = 1
	case "K":
		mult = 1 << 10
	case "M":
		mult = 1 << 20
	case "G":
		mult = 1 << 30
	case "T":
		mult = 1 << 40
	}
	// "GB"/"MB" without "i" are decimal.
	if len(unit) == 2 && unit[1] == 'B' {
		mult = map[byte]int64{'K': 1e3, 'M': 1e6, 'G': 1e9, 'T': 1e12}[unit[0]]
	}
	if n > (1<<53)/mult {
		return 0, fmt.Errorf("size %q too large", s)
	}
	return n * mult, nil
}

// ParseDuration parses Go durations plus a "d" (day) suffix, whole seconds only.
func ParseDuration(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if d, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseInt(d, 10, 64)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return n * 86400, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 || d%time.Second != 0 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return int64(d / time.Second), nil
}
