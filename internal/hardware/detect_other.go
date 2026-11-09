//go:build !windows && !linux && !darwin

package hardware

func platformRAM() (total, available uint64) { return 0, 0 }

func platformCPU() string { return "" }

func platformPhysicalCores() int { return 0 }

func platformGPU() []GPU { return nil }
