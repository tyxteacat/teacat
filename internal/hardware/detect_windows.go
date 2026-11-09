//go:build windows

package hardware

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
)

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

func platformRAM() (total, available uint64) {
	var ms memoryStatusEx
	ms.Length = uint32(unsafe.Sizeof(ms))
	r1, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms)))
	if r1 == 0 {
		return 0, 0
	}
	return ms.TotalPhys, ms.AvailPhys
}

func platformCPU() string {
	if name := runPowerShell(`(Get-CimInstance Win32_Processor | Select-Object -First 1).Name`); name != "" {
		return name
	}
	return os.Getenv("PROCESSOR_IDENTIFIER")
}

func platformGPU() []GPU {
	out := runPowerShell(`Get-CimInstance Win32_VideoController | ForEach-Object { $_.Name }`)
	var gpus []GPU
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		gpus = append(gpus, GPU{Name: name, Vendor: gpuVendor(name)})
	}
	return gpus
}

func runPowerShell(script string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gpuVendor(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "nvidia"), strings.Contains(lower, "geforce"), strings.Contains(lower, "quadro"):
		return "NVIDIA"
	case strings.Contains(lower, "amd"), strings.Contains(lower, "radeon"), strings.Contains(lower, "ati"):
		return "AMD"
	case strings.Contains(lower, "intel"), strings.Contains(lower, "arc"), strings.Contains(lower, "iris"), strings.Contains(lower, "uhd"):
		return "Intel"
	case strings.Contains(lower, "apple"):
		return "Apple"
	}
	return ""
}
