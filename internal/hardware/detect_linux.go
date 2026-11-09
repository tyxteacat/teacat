//go:build linux

package hardware

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func platformRAM() (total, available uint64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		kb, _ := strconv.ParseUint(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			total = kb * 1024
		case "MemAvailable:":
			available = kb * 1024
		}
	}
	return total, available
}

func platformCPU() string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "model name") || strings.HasPrefix(line, "Hardware") {
			if i := strings.Index(line, ":"); i >= 0 {
				return strings.TrimSpace(line[i+1:])
			}
		}
	}
	return ""
}

func platformPhysicalCores() int {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	seen := map[string]bool{}
	physID, coreID := "", ""
	flush := func() {
		if physID != "" && coreID != "" {
			seen[physID+"/"+coreID] = true
		}
		physID, coreID = "", ""
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		switch strings.TrimSpace(parts[0]) {
		case "physical id":
			physID = strings.TrimSpace(parts[1])
		case "core id":
			coreID = strings.TrimSpace(parts[1])
		}
	}
	flush()
	return len(seen)
}

func platformGPU() []GPU {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sh", "-c", "lspci 2>/dev/null | grep -Ei 'vga|3d|display'").Output()
	if err != nil {
		return nil
	}
	var gpus []GPU
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name := line
		if i := strings.Index(line, ": "); i >= 0 {
			name = line[i+2:]
		}
		gpus = append(gpus, GPU{Name: name, Vendor: gpuVendor(name)})
	}
	return gpus
}

func gpuVendor(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "nvidia"), strings.Contains(lower, "geforce"), strings.Contains(lower, "quadro"):
		return "NVIDIA"
	case strings.Contains(lower, "amd"), strings.Contains(lower, "radeon"), strings.Contains(lower, "ati"):
		return "AMD"
	case strings.Contains(lower, "intel"):
		return "Intel"
	}
	return ""
}
