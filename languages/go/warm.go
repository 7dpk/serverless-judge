// Compiled once at image build so the standard packages submissions usually
// import are already in the build cache.
package main

import (
	"bufio"
	"bytes"
	"container/heap"
	"container/list"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"math/big"
	"math/bits"
	"math/rand"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

type h []int

func (x h) Len() int           { return len(x) }
func (x h) Less(i, j int) bool { return x[i] < x[j] }
func (x h) Swap(i, j int)      { x[i], x[j] = x[j], x[i] }
func (x *h) Push(v any)        { *x = append(*x, v.(int)) }
func (x *h) Pop() any          { o := *x; v := o[len(o)-1]; *x = o[:len(o)-1]; return v }

func main() {
	r := bufio.NewReader(os.Stdin)
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	var n int
	fmt.Fscan(r, &n)
	s := []int{n}
	sort.Ints(s)
	slices.Sort(s)
	q := &h{}
	heap.Push(q, 1)
	l := list.New()
	l.PushBack(1)
	var mu sync.Mutex
	mu.Lock()
	mu.Unlock()
	b, _ := json.Marshal(map[string]int{"a": 1})
	m := maps.Clone(map[int]int{})
	_ = errors.New("x")
	_, _ = io.Discard.Write(b)
	fmt.Fprintln(w, strings.Repeat("x", 1), strconv.Itoa(n), math.Abs(1), big.NewInt(1), bits.Len(1),
		rand.Intn(2), regexp.MustCompile("a").MatchString("a"), time.Now().IsZero(), unicode.IsDigit('1'),
		bytes.Equal(nil, nil), len(m))
}
