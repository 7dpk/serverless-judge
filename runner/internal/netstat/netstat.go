// Package netstat reads how much has arrived over the network.
package netstat

import (
	"bytes"
	"os"
	"strconv"
)

// RxBytes is the total received on every interface except loopback, as the
// kernel counts it: payload plus protocol overhead. ok is false where the
// counters do not exist (anything that is not Linux).
func RxBytes() (n uint64, ok bool) {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, false
	}
	return Parse(data), true
}

// Parse sums the receive-bytes column of /proc/net/dev.
func Parse(data []byte) uint64 {
	var total uint64
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		name, rest, found := bytes.Cut(line, []byte{':'})
		if !found {
			continue // the two header lines
		}
		if string(bytes.TrimSpace(name)) == "lo" {
			continue
		}
		fields := bytes.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		if v, err := strconv.ParseUint(string(fields[0]), 10, 64); err == nil {
			total += v
		}
	}
	return total
}
