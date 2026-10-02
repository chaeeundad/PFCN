// Package capability detects local hardware and builds signed capability
// documents (spec §11).
package capability

import (
	"bufio"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// Hardware is the locally detected machine description. Only coarse,
// non-identifying fields are advertised (§11.4).
type Hardware struct {
	CPUModel    string
	Cores       int
	MemoryBytes int64
}

// Detect inspects the local machine.
func Detect() Hardware {
	h := Hardware{Cores: runtime.NumCPU(), CPUModel: "unknown"}
	switch runtime.GOOS {
	case "linux":
		if f, err := os.Open("/proc/cpuinfo"); err == nil {
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				k, v, ok := strings.Cut(sc.Text(), ":")
				if ok && (strings.TrimSpace(k) == "model name" || strings.TrimSpace(k) == "Model") {
					h.CPUModel = strings.TrimSpace(v)
					break
				}
			}
			f.Close()
		}
		if f, err := os.Open("/proc/meminfo"); err == nil {
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				if fields := strings.Fields(sc.Text()); len(fields) >= 2 && fields[0] == "MemTotal:" {
					kb, _ := strconv.ParseInt(fields[1], 10, 64)
					h.MemoryBytes = kb * 1024
					break
				}
			}
			f.Close()
		}
	case "darwin":
		if out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output(); err == nil {
			h.CPUModel = strings.TrimSpace(string(out))
		}
		if out, err := exec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil {
			h.MemoryBytes, _ = strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		}
	}
	return h
}
