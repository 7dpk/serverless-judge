// Package proto holds the wire types shared by the API service, the runner
// HTTP server and the in-sandbox supervisor.
package proto

// Step is one command a language manifest asks the supervisor to run.
// Zero limits fall back to the supervisor defaults for that kind of step.
type Step struct {
	Cmd      []string `json:"cmd"`
	CPUMs    int      `json:"cpu_ms,omitempty"`
	WallMs   int      `json:"wall_ms,omitempty"`
	MemoryKB int      `json:"memory_kb,omitempty"`
	// Daemon leaves the step's processes running when it returns. Only
	// honoured for warmup; prepare always behaves this way.
	Daemon bool `json:"daemon,omitempty"`
}

// Manifest describes how one language is compiled and run. It lives next to
// the language's Dockerfile as <name>.json and is copied into the image.
//
// Command tokens support these placeholders:
//
//	{options}    expands to the caller's compiler_options, word-split
//	{args}       expands to the caller's command line arguments, word-split
//	{memory_mb}  the memory limit in MiB (in-token replacement)
//	{memory_kb}  the memory limit in KiB (in-token replacement)
//	{cpu_s}      the CPU limit in whole seconds, rounded up (in-token)
type Manifest struct {
	Name       string `json:"name"`        // e.g. "cpp"
	Title      string `json:"title"`       // e.g. "C++ (GCC 14, C++20)"
	IDs        []int  `json:"ids"`         // Judge0-style language ids; first is primary
	SourceFile string `json:"source_file"` // e.g. "main.cpp"

	Compile   *Step `json:"compile,omitempty"`    // once per submission
	Prepare   *Step `json:"prepare,omitempty"`    // once per submission, may leave daemons running
	BeforeRun *Step `json:"before_run,omitempty"` // per test, untimed, receives the test's stdin
	Run       Step  `json:"run"`                  // per test, timed and limited
	AfterRun  *Step `json:"after_run,omitempty"`  // per test, untimed cleanup, always runs
	// Warmup runs once when a sandbox is created, before any job arrives, to
	// load the toolchain into the sandbox's file cache.
	Warmup *Step `json:"warmup,omitempty"`

	Env map[string]string `json:"env,omitempty"`

	// StackKB raises the main thread's stack limit for the run step. Native
	// languages set it so deep recursion behaves as it does on Codeforces.
	StackKB int `json:"stack_kb,omitempty"`

	// WallClock makes the time limit apply to wall time instead of CPU time.
	// Used for SQL, where the work happens in the database engine.
	WallClock bool `json:"wall_clock,omitempty"`
	// NoMemoryLimit turns off the RSS limit for the run step.
	NoMemoryLimit bool `json:"no_memory_limit,omitempty"`
	// AddressSpace additionally caps virtual memory (RLIMIT_AS) at the memory
	// limit plus AddressSpaceExtraKB. Only suitable for native code.
	AddressSpace        bool `json:"address_space,omitempty"`
	AddressSpaceExtraKB int  `json:"address_space_extra_kb,omitempty"`

	// RuntimeCPUMs is CPU time the language's own runtime spends starting
	// and stopping, measured in a sandbox for a program that does nothing.
	// It is added to a test's CPU limit and taken off the time reported, so
	// that a language with a slow start is judged on the program's work.
	// Set only where the start is a large part of the limit (the Erlang VM,
	// Clojure, Groovy); no effect on wall-clock languages.
	RuntimeCPUMs int `json:"runtime_cpu_ms,omitempty"`
}

// Limits apply to each test's run step.
type Limits struct {
	CPUMs    int `json:"cpu_ms"`
	WallMs   int `json:"wall_ms"`
	MemoryKB int `json:"memory_kb"`
	OutputKB int `json:"output_kb"` // cap on stdout and on stderr, each
	Procs    int `json:"procs"`     // processes plus threads
}

type Test struct {
	Stdin []byte `json:"stdin,omitempty"`
}

// Network asks for a sandbox that can reach the internet, and carries the
// limits that come with that. Without it a sandbox has no network at all,
// which is the default.
type Network struct {
	WallMs int `json:"wall_ms"` // added to each test's wall limit, for time spent waiting on the network
	RxKB   int `json:"rx_kb"`   // most a test may receive over the network
	FileKB int `json:"file_kb"` // largest file a test may write, in place of the output limit
	// Receiving data costs CPU that is not the program's own work. This
	// much is added to the CPU limit, and taken off the reported time, for
	// every MB received.
	CPUUsPerMB int `json:"cpu_us_per_mb"`
}

// RunRequest is what the API sends to a runner: one program, many inputs.
type RunRequest struct {
	Language        string            `json:"language"`
	Source          []byte            `json:"source"`
	Files           map[string][]byte `json:"files,omitempty"`
	CompilerOptions string            `json:"compiler_options,omitempty"`
	Args            string            `json:"args,omitempty"`
	Limits          Limits            `json:"limits"`
	Tests           []Test            `json:"tests"`
	MergeStderr     bool              `json:"merge_stderr,omitempty"`
	Network         *Network          `json:"network,omitempty"`
}

// Phase is the outcome of a compile or prepare step.
type Phase struct {
	OK       bool   `json:"ok"`
	Output   []byte `json:"output,omitempty"`
	ExitCode int    `json:"exit_code"`
	Signal   int    `json:"signal,omitempty"`
	Killed   string `json:"killed,omitempty"` // "wall", "cpu" or "mem"
	CPUMs    int    `json:"cpu_ms"`
	WallMs   int    `json:"wall_ms"`
	MemoryKB int    `json:"memory_kb"`
}

// Test statuses.
const (
	StatusOK  = "ok"  // exited 0
	StatusRE  = "re"  // non-zero exit or killed by a signal
	StatusTLE = "tle" // time limit exceeded
	StatusMLE = "mle" // memory limit exceeded
	StatusOLE = "ole" // output limit exceeded
	StatusIE  = "ie"  // the judge could not run the test
)

type TestResult struct {
	Status   string `json:"status"`
	ExitCode int    `json:"exit_code"`
	Signal   int    `json:"signal,omitempty"`
	Stdout   []byte `json:"stdout,omitempty"`
	Stderr   []byte `json:"stderr,omitempty"`
	CPUMs    int    `json:"cpu_ms"`
	WallMs   int    `json:"wall_ms"`
	MemoryKB int    `json:"memory_kb"`
	Message  string `json:"message,omitempty"`
	// Set only for a run with network access.
	NetRxKB     int `json:"net_rx_kb,omitempty"`     // received over the network
	NetCreditMs int `json:"net_credit_ms,omitempty"` // CPU time not counted because of it
}

type Meta struct {
	Warm          bool   `json:"warm"`            // a pre-created sandbox was ready
	SandboxWaitMs int    `json:"sandbox_wait_ms"` // time spent waiting for the sandbox
	BoxMs         int    `json:"box_ms"`          // time inside the sandbox
	TotalMs       int    `json:"total_ms"`        // whole request on the runner
	Instance      string `json:"instance,omitempty"`
	Network       bool   `json:"network,omitempty"`    // the sandbox had network access
	HostRxKB      int    `json:"host_rx_kb,omitempty"` // received by the whole instance meanwhile
}

type RunResponse struct {
	Compile *Phase       `json:"compile,omitempty"`
	Tests   []TestResult `json:"tests,omitempty"`
	Error   string       `json:"error,omitempty"` // internal error, nothing ran to completion
	Meta    Meta         `json:"meta"`
}

// Job is what the runner writes to the supervisor's stdin.
type Job struct {
	Nonce    string            `json:"nonce"`
	Manifest Manifest          `json:"manifest"`
	Req      RunRequest        `json:"req"`
	Env      map[string]string `json:"env,omitempty"`
}
