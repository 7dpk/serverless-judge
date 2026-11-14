//go:build !linux

package box

// The supervisor only runs on Linux. These stubs let the pure logic in this
// package build and be unit tested on a developer machine.

type procInfo struct {
	pid, ppid int
	state     byte
	rssKB     int
	cpuMs     int
}

func setNonDumpable()                          {}
func listPids() []int                          { return nil }
func readProc(pid int) (procInfo, bool)        { return procInfo{}, false }
func readSmaps(pid int) (int, int, smapsState) { return 0, 0, smapsDenied }
func diskUsedKB() int                          { return 0 }
