//go:build darwin

package hardware

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func platformRAM() (total, available uint64) {
	out := run("sysctl", "-n", "hw.memsize")
	if n, err := strconv.ParseUint(strings.TrimSpace(out), 10, 64); err == nil {
		// macOS does not expose a simple "available" figure here; report the
		// total and let callers treat 0 as unknown.
		return n, 0
	}
	return 0, 0
}

func platformCPU() string {
	return run("sysctl", "-n", "machdep.cpu.brand_string")
}

func platformPhysicalCores() int {
	n, _ := strconv.Atoi(run("sysctl", "-n", "hw.physicalcpu"))
	return n
}

func platformGPU() []GPU {
	out := run("system_profiler", "SPDisplaysDataType")
	var gpus []GPU
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Chipset Model:") && !strings.HasPrefix(line, "Model:") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[1])
		if name != "" {
			gpus = append(gpus, GPU{Name: name, Vendor: gpuVendor(name)})
		}
	}
	return gpus
}

func run(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gpuVendor(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "nvidia"), strings.Contains(lower, "geforce"):
		return "NVIDIA"
	case strings.Contains(lower, "amd"), strings.Contains(lower, "radeon"):
		return "AMD"
	case strings.Contains(lower, "intel"):
		return "Intel"
	case strings.Contains(lower, "apple"):
		return "Apple"
	}
	return ""
}
