//go:build !darwin

package switcher

func (r MacRuntime) scopedProcesses() (map[int]codexProcess, error) { return readProcesses() }
