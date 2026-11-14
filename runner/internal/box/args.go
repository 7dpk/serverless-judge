package box

import (
	"strconv"
	"strings"
)

// SplitWords splits a caller-supplied option string into argv tokens using
// shell-like quoting. Nothing is ever passed to a shell.
func SplitWords(s string) []string {
	var out []string
	var cur strings.Builder
	inTok := false
	var quote rune
	esc := false
	for _, r := range s {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			switch r {
			case '"':
				quote = 0
			case '\\':
				esc = true
			default:
				cur.WriteRune(r)
			}
		case r == '\\':
			esc = true
			inTok = true
		case r == '\'' || r == '"':
			quote = r
			inTok = true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if inTok {
				out = append(out, cur.String())
				cur.Reset()
				inTok = false
			}
		default:
			cur.WriteRune(r)
			inTok = true
		}
	}
	if inTok {
		out = append(out, cur.String())
	}
	return out
}

// Vars are the values available to manifest command templates.
type Vars struct {
	Options  string
	Args     string
	MemoryKB int
	CPUMs    int
	Test     int
}

// Expand turns a manifest command template into a concrete argv.
func Expand(tmpl []string, v Vars) []string {
	rep := strings.NewReplacer(
		"{memory_mb}", strconv.Itoa(v.MemoryKB/1024),
		"{memory_kb}", strconv.Itoa(v.MemoryKB),
		"{cpu_s}", strconv.Itoa((v.CPUMs+999)/1000),
		"{test}", strconv.Itoa(v.Test),
	)
	out := make([]string, 0, len(tmpl)+4)
	for _, t := range tmpl {
		switch t {
		case "{options}":
			out = append(out, SplitWords(v.Options)...)
		case "{args}":
			out = append(out, SplitWords(v.Args)...)
		default:
			out = append(out, rep.Replace(t))
		}
	}
	return out
}
