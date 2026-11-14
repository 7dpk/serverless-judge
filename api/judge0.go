package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"serverless-judge/runner/proto"
)

// Status ids match Judge0, plus 15 for the Codeforces-style memory verdict.
const (
	StInQueue      = 1
	StProcessing   = 2
	StAccepted     = 3
	StWrongAnswer  = 4
	StTLE          = 5
	StCompileError = 6
	StSIGSEGV      = 7
	StSIGXFSZ      = 8
	StSIGFPE       = 9
	StSIGABRT      = 10
	StNZEC         = 11
	StOtherRE      = 12
	StInternal     = 13
	StExecFormat   = 14
	StMLE          = 15
)

var statusNames = map[int]string{
	StInQueue: "In Queue", StProcessing: "Processing", StAccepted: "Accepted",
	StWrongAnswer: "Wrong Answer", StTLE: "Time Limit Exceeded",
	StCompileError: "Compilation Error", StSIGSEGV: "Runtime Error (SIGSEGV)",
	StSIGXFSZ: "Runtime Error (SIGXFSZ)", StSIGFPE: "Runtime Error (SIGFPE)",
	StSIGABRT: "Runtime Error (SIGABRT)", StNZEC: "Runtime Error (NZEC)",
	StOtherRE: "Runtime Error (Other)", StInternal: "Internal Error",
	StExecFormat: "Exec Format Error", StMLE: "Memory Limit Exceeded",
}

// flexStr accepts a JSON string, number or boolean. Judge0 clients are loose
// about types, especially for limits.
type flexStr struct {
	set bool
	val string
}

func (f *flexStr) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	f.set = true
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &f.val)
	}
	f.val = string(b)
	return nil
}

func (f flexStr) float() (float64, bool, error) {
	if !f.set || strings.TrimSpace(f.val) == "" {
		return 0, false, nil
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(f.val), 64)
	return v, true, err
}

func (f flexStr) truthy() bool {
	v := strings.ToLower(strings.TrimSpace(f.val))
	return f.set && (v == "true" || v == "1")
}

// Input is a Judge0 submission as sent by a client.
type Input struct {
	SourceCode     *string `json:"source_code"`
	LanguageID     flexStr `json:"language_id"`
	Stdin          *string `json:"stdin"`
	ExpectedOutput *string `json:"expected_output"`
	CompilerOpts   *string `json:"compiler_options"`
	Args           *string `json:"command_line_arguments"`
	CallbackURL    *string `json:"callback_url"`
	Files          *string `json:"additional_files"` // base64 zip, always
	CPUTimeLimit   flexStr `json:"cpu_time_limit"`
	WallTimeLimit  flexStr `json:"wall_time_limit"`
	MemoryLimit    flexStr `json:"memory_limit"`
	MaxFileSize    flexStr `json:"max_file_size"`
	MaxProcs       flexStr `json:"max_processes_and_or_threads"`
	RedirectStderr flexStr `json:"redirect_stderr_to_stdout"`
}

// Submission is the stored record. Blob fields hold raw bytes in memory and
// are gzip-packed by the store.
type Submission struct {
	Token      string `firestore:"token"`
	LanguageID int    `firestore:"language_id"`
	Group      string `firestore:"group"`

	Source      []byte `firestore:"source"`
	Stdin       []byte `firestore:"stdin"`
	Expected    []byte `firestore:"expected"`
	HasExpected bool   `firestore:"has_expected"`
	Files       []byte `firestore:"files"`

	CompilerOpts   string `firestore:"compiler_options"`
	Args           string `firestore:"args"`
	CallbackURL    string `firestore:"callback_url"`
	CPUMs          int    `firestore:"cpu_ms"`
	WallMs         int    `firestore:"wall_ms"`
	MemoryKB       int    `firestore:"memory_kb"`
	FileKB         int    `firestore:"file_kb"`
	Procs          int    `firestore:"procs"`
	RedirectStderr bool   `firestore:"redirect_stderr"`

	StatusID      int    `firestore:"status_id"`
	Stdout        []byte `firestore:"stdout"`
	Stderr        []byte `firestore:"stderr"`
	CompileOutput []byte `firestore:"compile_output"`
	Message       string `firestore:"message"`
	TimeMs        int    `firestore:"time_ms"`
	WallTimeMs    int    `firestore:"wall_time_ms"`
	UsedKB        int    `firestore:"used_kb"`
	ExitCode      int    `firestore:"exit_code"`
	ExitSignal    int    `firestore:"exit_signal"`
	Ran           bool   `firestore:"ran"` // a test actually executed

	CreatedAt  time.Time `firestore:"created_at"`
	FinishedAt time.Time `firestore:"finished_at"`
	ExpireAt   time.Time `firestore:"expire_at"`
	// ClaimedAt is no longer written. It stays so that documents stored by
	// earlier versions still load.
	ClaimedAt time.Time `firestore:"claimed_at"`

	// Where the time went for this run. Not stored; reported to wait=true
	// callers in the Server-Timing header.
	timing runTiming `firestore:"-"`
}

type runTiming struct {
	rpcMs    int // API to runner and back
	runnerMs int // whole request inside the runner
	boxMs    int // compile and tests inside the sandbox
	waitMs   int // waiting for a sandbox
	warm     bool
}

func (s *Submission) done() bool { return s.StatusID > StProcessing }

// expired reports whether the submission is past its retention. Firestore
// removes such documents on its own schedule, up to a day later; until then
// the API already treats them as gone.
func (s *Submission) expired(now time.Time) bool {
	return !s.ExpireAt.IsZero() && now.After(s.ExpireAt)
}

// fieldErrors is Judge0's 422 body: {"field": ["message"]}.
type fieldErrors map[string][]string

func (e fieldErrors) add(field, msg string) { e[field] = append(e[field], msg) }

func decodeText(s *string, b64 bool, field string, errs fieldErrors) []byte {
	if s == nil {
		return nil
	}
	if !b64 {
		return []byte(*s)
	}
	out, err := b64decode(*s)
	if err != nil {
		errs.add(field, "is not valid base64")
		return nil
	}
	return out
}

// b64decode tolerates the line breaks and missing padding that common
// encoders produce (Ruby's encode64 wraps every 60 characters).
func b64decode(s string) ([]byte, error) {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, s)
	s = strings.TrimRight(s, "=")
	if strings.ContainsAny(s, "-_") {
		return base64.RawURLEncoding.DecodeString(s)
	}
	return base64.RawStdEncoding.DecodeString(s)
}

// build validates a client submission against the configured limits.
func (a *App) build(in *Input, b64 bool, now time.Time) (*Submission, fieldErrors) {
	c := a.cfg
	errs := fieldErrors{}
	s := &Submission{Token: newToken(), StatusID: StInQueue, CreatedAt: now, ExpireAt: now.Add(c.TTL)}

	if id, ok, err := in.LanguageID.float(); !ok || err != nil {
		errs.add("language_id", "can't be blank")
	} else if a.langs.get(int(id)) == nil {
		errs.add("language_id", fmt.Sprintf("language with id %d doesn't exist", int(id)))
	} else {
		s.LanguageID = int(id)
	}

	s.Source = decodeText(in.SourceCode, b64, "source_code", errs)
	if in.SourceCode == nil || len(s.Source) == 0 {
		errs.add("source_code", "can't be blank")
	}
	if len(s.Source) > c.MaxSourceKB*1024 {
		errs.add("source_code", fmt.Sprintf("must be at most %d KB", c.MaxSourceKB))
	}
	s.Stdin = decodeText(in.Stdin, b64, "stdin", errs)
	if len(s.Stdin) > c.MaxStdinKB*1024 {
		errs.add("stdin", fmt.Sprintf("must be at most %d KB", c.MaxStdinKB))
	}
	if in.ExpectedOutput != nil {
		s.Expected = decodeText(in.ExpectedOutput, b64, "expected_output", errs)
		s.HasExpected = true
		if len(s.Expected) > c.MaxStdinKB*1024 {
			errs.add("expected_output", fmt.Sprintf("must be at most %d KB", c.MaxStdinKB))
		}
	}
	if in.Files != nil && *in.Files != "" {
		raw, err := b64decode(*in.Files)
		if err != nil {
			errs.add("additional_files", "is not valid base64")
		} else if _, err := unzip(raw); err != nil {
			errs.add("additional_files", err.Error())
		} else {
			s.Files = raw
		}
	}
	if in.CompilerOpts != nil {
		s.CompilerOpts = *in.CompilerOpts
	}
	if in.Args != nil {
		s.Args = *in.Args
	}
	if len(s.CompilerOpts) > 512 {
		errs.add("compiler_options", "is too long (maximum is 512 characters)")
	}
	if len(s.Args) > 512 {
		errs.add("command_line_arguments", "is too long (maximum is 512 characters)")
	}
	if in.CallbackURL != nil && *in.CallbackURL != "" {
		if err := checkCallbackURL(*in.CallbackURL); err != nil {
			errs.add("callback_url", err.Error())
		}
		s.CallbackURL = *in.CallbackURL
	}

	limit := func(f flexStr, field string, def, max float64) float64 {
		v, ok, err := f.float()
		switch {
		case err != nil:
			errs.add(field, "is not a number")
			return def
		case !ok:
			return def
		case v <= 0:
			errs.add(field, "must be greater than 0")
			return def
		case v > max:
			errs.add(field, "must be less than or equal to "+strconv.FormatFloat(max, 'f', -1, 64))
			return def
		}
		return v
	}
	cpu := limit(in.CPUTimeLimit, "cpu_time_limit", c.DefaultCPU, c.MaxCPU)
	wall := limit(in.WallTimeLimit, "wall_time_limit", min(2*cpu+1, c.MaxWall), c.MaxWall)
	s.CPUMs = int(cpu*1000 + 0.5)
	s.WallMs = int(wall*1000 + 0.5)
	s.MemoryKB = int(limit(in.MemoryLimit, "memory_limit", float64(c.DefaultMemKB), float64(c.MaxMemKB)))
	s.FileKB = int(limit(in.MaxFileSize, "max_file_size", float64(c.DefaultFileKB), float64(c.MaxFileKB)))
	s.Procs = int(limit(in.MaxProcs, "max_processes_and_or_threads", float64(c.DefaultProcs), float64(c.MaxProcs)))
	s.RedirectStderr = in.RedirectStderr.truthy()

	if len(errs) > 0 {
		return nil, errs
	}
	s.Group = groupKey(s)
	return s, nil
}

// groupKey is equal for submissions that are the same program under the
// same limits, differing only in stdin and expected output. A batch of those
// is compiled once and run as several tests in one sandbox.
func groupKey(s *Submission) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d\x00%s\x00%s\x00%d\x00%d\x00%d\x00%d\x00%d\x00%t\x00",
		s.LanguageID, s.CompilerOpts, s.Args, s.CPUMs, s.WallMs, s.MemoryKB, s.FileKB, s.Procs, s.RedirectStderr)
	h.Write(s.Source)
	h.Write([]byte{0})
	h.Write(s.Files)
	return hex.EncodeToString(h.Sum(nil)[:12])
}

const (
	maxZipEntries = 64
	maxZipBytes   = 8 << 20
)

// unzip expands additional_files with hard caps, so a zip bomb cannot
// exhaust the API's memory.
func unzip(raw []byte) (map[string][]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("is not a valid zip archive")
	}
	if len(zr.File) > maxZipEntries {
		return nil, fmt.Errorf("has more than %d entries", maxZipEntries)
	}
	out := map[string][]byte{}
	total := 0
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("is not a valid zip archive")
		}
		data, err := io.ReadAll(io.LimitReader(rc, int64(maxZipBytes-total)+1))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("is not a valid zip archive")
		}
		total += len(data)
		if total > maxZipBytes {
			return nil, fmt.Errorf("expands to more than %d MB", maxZipBytes>>20)
		}
		out[f.Name] = data
	}
	return out, nil
}

// normalize mirrors Judge0's comparison: trailing whitespace is ignored on
// every line and at the end of the output.
func normalize(b []byte) string {
	const ws = " \t\r\n\v\f\x00"
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, ws)
	}
	return strings.TrimRight(strings.Join(lines, "\n"), ws)
}

// apply copies one test result into the submission and decides its status.
func (s *Submission) apply(t *proto.TestResult, now time.Time) {
	s.Ran = true
	s.Stdout, s.Stderr = t.Stdout, t.Stderr
	s.TimeMs, s.WallTimeMs, s.UsedKB = t.CPUMs, t.WallMs, t.MemoryKB
	s.ExitCode, s.ExitSignal = t.ExitCode, t.Signal
	s.FinishedAt = now
	switch t.Status {
	case proto.StatusOK:
		if !s.HasExpected || normalize(t.Stdout) == normalize(s.Expected) {
			s.StatusID = StAccepted
		} else {
			s.StatusID = StWrongAnswer
		}
	case proto.StatusTLE:
		s.StatusID, s.Message = StTLE, t.Message
	case proto.StatusMLE:
		s.StatusID, s.Message = StMLE, t.Message
	case proto.StatusOLE:
		s.StatusID, s.Message = StSIGXFSZ, t.Message
	case proto.StatusRE:
		s.Message = t.Message
		switch {
		case t.Signal == 11:
			s.StatusID = StSIGSEGV
		case t.Signal == 25:
			s.StatusID = StSIGXFSZ
		case t.Signal == 8:
			s.StatusID = StSIGFPE
		case t.Signal == 6:
			s.StatusID = StSIGABRT
		case t.Signal != 0:
			s.StatusID = StOtherRE
		case t.ExitCode == 126:
			s.StatusID = StExecFormat
		default:
			s.StatusID = StNZEC
		}
	default:
		s.Ran = false
		s.StatusID, s.Message = StInternal, t.Message
	}
}

func (s *Submission) fail(status int, msg string, now time.Time) {
	s.StatusID, s.Message, s.FinishedAt = status, msg, now
}

var defaultFields = []string{"stdout", "time", "memory", "stderr", "token", "compile_output", "message", "status"}

var allFields = []string{
	"source_code", "language_id", "compiler_options", "command_line_arguments", "stdin",
	"expected_output", "cpu_time_limit", "cpu_extra_time", "wall_time_limit", "memory_limit",
	"stack_limit", "max_processes_and_or_threads", "enable_per_process_and_thread_time_limit",
	"enable_per_process_and_thread_memory_limit", "max_file_size", "redirect_stderr_to_stdout",
	"enable_network", "number_of_runs", "additional_files", "callback_url", "stdout", "stderr",
	"compile_output", "message", "exit_code", "exit_signal", "status", "created_at", "finished_at",
	"token", "time", "wall_time", "memory", "status_id", "language",
}

func parseFields(q string) ([]string, error) {
	if q == "" {
		return defaultFields, nil
	}
	if q == "*" {
		return allFields, nil
	}
	known := map[string]bool{}
	for _, f := range allFields {
		known[f] = true
	}
	var out, bad []string
	for _, f := range strings.Split(q, ",") {
		if f = strings.TrimSpace(f); f == "" {
			continue
		}
		if !known[f] {
			bad = append(bad, f)
		}
		out = append(out, f)
	}
	if len(bad) > 0 {
		return nil, fmt.Errorf("invalid fields: [%s]", strings.Join(bad, ", "))
	}
	return out, nil
}

func secs(ms int) string { return strconv.FormatFloat(float64(ms)/1000, 'f', 3, 64) }

func stamp(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// render builds the JSON view of a submission with the requested fields.
// Like Judge0, empty outputs are null, and text is base64 when asked.
func (a *App) render(s *Submission, fields []string, b64 bool) map[string]any {
	text := func(b []byte) any {
		if len(b) == 0 {
			return nil
		}
		if b64 {
			return base64.StdEncoding.EncodeToString(b)
		}
		if utf8.Valid(b) {
			return string(b)
		}
		return strings.ToValidUTF8(string(b), "�")
	}
	str := func(v string) any {
		if v == "" {
			return nil
		}
		return text([]byte(v))
	}
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		var v any
		switch f {
		case "token":
			v = s.Token
		case "source_code":
			v = text(s.Source)
		case "stdin":
			v = text(s.Stdin)
		case "expected_output":
			if s.HasExpected {
				v = text(s.Expected)
			}
		case "stdout":
			v = text(s.Stdout)
		case "stderr":
			v = text(s.Stderr)
		case "compile_output":
			v = text(s.CompileOutput)
		case "message":
			v = str(s.Message)
		case "status":
			v = map[string]any{"id": s.StatusID, "description": statusNames[s.StatusID]}
		case "status_id":
			v = s.StatusID
		case "language_id":
			v = s.LanguageID
		case "language":
			name := ""
			if l := a.langs.get(s.LanguageID); l != nil {
				name = l.Name
			}
			v = map[string]any{"id": s.LanguageID, "name": name}
		case "time":
			if s.Ran {
				v = secs(s.TimeMs)
			}
		case "wall_time":
			if s.Ran {
				v = secs(s.WallTimeMs)
			}
		case "memory":
			if s.Ran {
				v = s.UsedKB
			}
		case "exit_code":
			if s.Ran && s.ExitSignal == 0 {
				v = s.ExitCode
			}
		case "exit_signal":
			if s.Ran && s.ExitSignal != 0 {
				v = s.ExitSignal
			}
		case "created_at":
			v = stamp(s.CreatedAt)
		case "finished_at":
			v = stamp(s.FinishedAt)
		case "cpu_time_limit":
			v = secs(s.CPUMs)
		case "wall_time_limit":
			v = secs(s.WallMs)
		case "cpu_extra_time":
			v = "0.0"
		case "memory_limit":
			v = s.MemoryKB
		case "stack_limit":
			v = s.MemoryKB
		case "max_file_size":
			v = s.FileKB
		case "max_processes_and_or_threads":
			v = s.Procs
		case "compiler_options":
			v = str(s.CompilerOpts)
		case "command_line_arguments":
			v = str(s.Args)
		case "callback_url":
			if s.CallbackURL != "" {
				v = s.CallbackURL
			}
		case "redirect_stderr_to_stdout":
			v = s.RedirectStderr
		case "additional_files":
			if len(s.Files) > 0 {
				v = base64.StdEncoding.EncodeToString(s.Files)
			}
		case "number_of_runs":
			v = 1
		case "enable_network", "enable_per_process_and_thread_time_limit", "enable_per_process_and_thread_memory_limit":
			v = false
		}
		out[f] = v
	}
	return out
}
