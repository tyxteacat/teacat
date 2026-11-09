// Package hardware detects the machine TeaCat is running on so the runtime can
// use the available CPU cores and RAM sensibly. Inference currently runs on the
// CPU; GPUs are detected and reported but are not used for tensor math yet.
package hardware

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
)

// GPU describes one graphics adapter found on the machine.
type GPU struct {
	Name        string `json:"name"`
	Vendor      string `json:"vendor,omitempty"`
	MemoryBytes uint64 `json:"memory_bytes,omitempty"`
}

// Info is a snapshot of the host hardware.
type Info struct {
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	CPU           string `json:"cpu"`
	Cores         int    `json:"cores"`
	PhysicalCores int    `json:"physical_cores"`
	GOMAXPROCS    int    `json:"gomaxprocs"`
	RAMTotal      uint64 `json:"ram_total"`
	RAMAvailable  uint64 `json:"ram_available"`
	GPUs          []GPU  `json:"gpus,omitempty"`
	Backend       string `json:"backend"`
}

// Detect returns the current hardware snapshot. CPU/GPU lookups may spawn a
// short platform command on some operating systems, so call it for `info` and
// server startup rather than for every token.
func Detect() Info {
	total, avail := platformRAM()
	gpus := platformGPU()
	info := Info{
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		CPU:           platformCPU(),
		Cores:         runtime.NumCPU(),
		PhysicalCores: PhysicalCores(),
		GOMAXPROCS:    runtime.GOMAXPROCS(0),
		RAMTotal:      total,
		RAMAvailable:  avail,
		GPUs:          gpus,
		Backend:       "CPU (pure Go)",
	}
	if info.CPU == "" {
		info.CPU = runtime.GOARCH + " CPU"
	}
	return info
}

var (
	physOnce  sync.Once
	physCores int
)

// PhysicalCores returns the number of physical CPU cores. Using physical
// rather than logical (SMT) cores is often faster for memory-bound matvec work.
func PhysicalCores() int {
	physOnce.Do(func() {
		physCores = platformPhysicalCores()
		if physCores <= 0 || physCores > runtime.NumCPU() {
			physCores = runtime.NumCPU()
		}
	})
	return physCores
}

// Summary is a one-line description used in server startup logs.
func (i Info) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s, %d cores / %d threads", i.CPU, i.PhysicalCores, i.Cores)
	if i.RAMTotal > 0 {
		fmt.Fprintf(&b, ", %s RAM", HumanBytes(i.RAMTotal))
	}
	if len(i.GPUs) > 0 {
		fmt.Fprintf(&b, ", GPU: %s (CPU inference)", i.GPUs[0].Name)
	}
	return b.String()
}

// NeedBytes estimates the RAM needed to run a model file of size bytes. F16 and
// BF16 weights are expanded to F32 at load time unless low-memory mode is used,
// so those need roughly twice their file size plus working memory.
func NeedBytes(size uint64, quant string) uint64 {
	need := size
	q := strings.ToUpper(quant)
	if q == "" || strings.Contains(q, "F16") || strings.Contains(q, "BF16") {
		need = size * 2
	}
	const overhead = 192 << 20 // KV cache, tokenizer, scratch buffers
	return need + overhead
}

// ShouldUseLowMemory reports whether a model should be loaded in packed-weight
// mode to avoid exhausting RAM. It returns false when available RAM is unknown.
func ShouldUseLowMemory(size uint64, quant string) bool {
	_, avail := platformRAM()
	if avail == 0 {
		return false
	}
	need := NeedBytes(size, quant)
	return need > avail*85/100
}

// HumanBytes formats a byte count for people.
func HumanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
