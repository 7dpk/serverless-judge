//go:build linux

package box

import (
	"bytes"
	"errors"
	"os"
	"strconv"
	"syscall"
)

const (
	prSetDumpable = 4
	clockTickMs   = 10 // USER_HZ is 100 on every Linux ABI we run on
)

var pageKB = os.Getpagesize() / 1024

// setNonDumpable stops other processes of the same user from reading this
// process's memory or opening its file descriptors through /proc.
func setNonDumpable() {
	syscall.RawSyscall(syscall.SYS_PRCTL, prSetDumpable, 0, 0)
}

type procInfo struct {
	pid, ppid int
	state     byte
	rssKB     int
	cpuMs     int
}

func listPids() []int {
	d, err := os.Open("/proc")
	if err != nil {
		return nil
	}
	defer d.Close()
	names, _ := d.Readdirnames(-1)
	pids := make([]int, 0, len(names))
	for _, n := range names {
		if n[0] < '0' || n[0] > '9' {
			continue
		}
		if pid, err := strconv.Atoi(n); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

// readProc parses /proc/<pid>/stat. The command name can contain spaces and
// parentheses, so fields are counted from the last ')'.
func readProc(pid int) (procInfo, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return procInfo{}, false
	}
	i := bytes.LastIndexByte(data, ')')
	if i < 0 || i+2 >= len(data) {
		return procInfo{}, false
	}
	f := bytes.Fields(data[i+2:])
	// f[0]=state f[1]=ppid f[11]=utime f[12]=stime f[21]=rss (pages)
	if len(f) < 22 {
		return procInfo{}, false
	}
	p := procInfo{pid: pid, state: f[0][0]}
	p.ppid, _ = strconv.Atoi(string(f[1]))
	ut, _ := strconv.Atoi(string(f[11]))
	st, _ := strconv.Atoi(string(f[12]))
	rss, _ := strconv.Atoi(string(f[21]))
	p.cpuMs = (ut + st) * clockTickMs
	p.rssKB = rss * pageKB
	return p, true
}

var (
	smapsRss  = []byte("Rss:")
	smapsAnon = []byte("Anonymous:")
)

// readSmaps sums resident and anonymous memory over a process's mappings.
func readSmaps(pid int) (anonKB, residentKB int, state smapsState) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/smaps")
	switch {
	case errors.Is(err, os.ErrPermission):
		return 0, 0, smapsDenied
	case err != nil || len(data) == 0:
		return 0, 0, smapsGone
	}
	for len(data) > 0 {
		line := data
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = data[:i], data[i+1:]
		} else {
			data = nil
		}
		switch {
		case bytes.HasPrefix(line, smapsRss):
			residentKB += smapsKB(line[len(smapsRss):])
		case bytes.HasPrefix(line, smapsAnon):
			anonKB += smapsKB(line[len(smapsAnon):])
		}
	}
	return anonKB, residentKB, smapsOK
}

// smapsKB parses "        1234 kB".
func smapsKB(b []byte) int {
	n := 0
	for _, c := range b {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		} else if n > 0 {
			break
		}
	}
	return n
}

// diskUsedKB is how much the sandbox's in-memory filesystems hold: /tmp, and
// the root overlay when the sandbox was created writable.
func diskUsedKB() int {
	total := 0
	seen := map[syscall.Fsid]bool{}
	for _, path := range []string{"/tmp", "/"} {
		var st syscall.Statfs_t
		if syscall.Statfs(path, &st) != nil || seen[st.Fsid] {
			continue
		}
		seen[st.Fsid] = true
		total += int((st.Blocks - st.Bfree) * uint64(st.Bsize) / 1024)
	}
	return total
}
