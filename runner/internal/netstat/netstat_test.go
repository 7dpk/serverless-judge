package netstat

import "testing"

func TestParse(t *testing.T) {
	// As printed inside a sandbox created with --allow-egress.
	const dev = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:     200       4    0    0    0     0          0         0      200       4    0    0    0     0       0          0
 tunl0:       0       0    0    0    0     0          0         0        0       0    0    0    0     0       0          0
ip6tnl0:       0       0    0    0    0     0          0         0        0       0    0    0    0     0       0          0
  eth0:    8982      23    0    0    0     0          0         0     5499      30    0    0    0     0       0          0
  eth1:1000000      23    0    0    0     0          0         0     5499      30    0    0    0     0       0          0
`
	if got := Parse([]byte(dev)); got != 8982+1000000 {
		t.Fatalf("Parse = %d, want %d", got, 8982+1000000)
	}
	if got := Parse([]byte("garbage\n")); got != 0 {
		t.Fatalf("Parse(garbage) = %d, want 0", got)
	}
}
