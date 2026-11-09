//go:build !windows && !linux && !darwin

package hardware

func platformRAM() (total, available uint64) { return 0, 0 }

func platformCPU() string { return "" }

func platformGPU() []GPU { return nil }
