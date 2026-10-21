package artifact

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	envOfficeChild        = "MULTICA_OFFICE_CHILD"
	envOfficeMode         = "MULTICA_OFFICE_MODE"
	envOfficeInput        = "MULTICA_OFFICE_INPUT"
	envOfficeFormat       = "MULTICA_OFFICE_FORMAT"
	envOfficeAllowNetwork = "MULTICA_OFFICE_ALLOW_NETWORK"
	envOfficeASBytes      = "MULTICA_OFFICE_AS_BYTES"
	envOfficeCPUSeconds   = "MULTICA_OFFICE_CPU_SECONDS"

	// DefaultOfficeWall bounds the child. The parent kills the process.
	DefaultOfficeWall = 15 * time.Second
	// DefaultOfficeASBytes is a hard address-space cap for a real conversion.
	DefaultOfficeASBytes = 2 << 30
	// DefaultOfficeCPUSeconds is RLIMIT_CPU for a real conversion.
	DefaultOfficeCPUSeconds = 5

	officeStdoutCap = 4 << 20
)

// OfficeLimits bounds one conversion child. Zero Wall, AS, and CPU pick the
// defaults for mode convert. Other modes apply a limit only when it is set.
type OfficeLimits struct {
	Wall       time.Duration
	ASBytes    uint64
	CPUSeconds uint64
	Mode       string
	Format     string
}

// OfficeResult is the child's report. HTML is escaped text, sanitized again
// by the parent before it is stored.
type OfficeResult struct {
	OK           bool
	ErrorCode    string
	PageCount    int
	SheetNames   []string
	SlideCount   int
	Text         string
	HTML         []byte
	EnvKeys      []string
	AllowNetwork bool
}

type officeChildReport struct {
	OK           bool     `json:"ok"`
	ErrorCode    string   `json:"error_code,omitempty"`
	PageCount    int      `json:"page_count,omitempty"`
	SheetNames   []string `json:"sheet_names,omitempty"`
	SlideCount   int      `json:"slide_count,omitempty"`
	Text         string   `json:"text,omitempty"`
	HTML         string   `json:"html,omitempty"`
	EnvKeys      []string `json:"env_keys,omitempty"`
	AllowNetwork bool     `json:"allow_network"`
}

// The conversion child is this binary. Exit before any database, telemetry,
// or listen socket in the re-exec'd process.
func init() {
	if os.Getenv(envOfficeChild) == "1" {
		os.Exit(RunOfficeChild())
	}
}

// RunOfficeChild is the isolated converter. It does not open a database.
func RunOfficeChild() int {
	applyOfficeRlimits()
	switch os.Getenv(envOfficeMode) {
	case "env":
		return writeOfficeReport(officeChildReport{
			OK:           true,
			AllowNetwork: false,
			EnvKeys:      officeEnvKeys(),
		})
	case "sleep":
		time.Sleep(30 * time.Second)
		return 0
	case "allocate":
		return officeAllocate()
	default:
		data, err := os.ReadFile(os.Getenv(envOfficeInput))
		if err != nil {
			return writeOfficeReport(officeChildReport{ErrorCode: CodeConversionFailed})
		}
		extracted, err := extractOffice(data, os.Getenv(envOfficeFormat))
		if errors.Is(err, errOfficeExhausted) {
			return writeOfficeReport(officeChildReport{ErrorCode: CodeConversionExhausted})
		}
		if err != nil {
			return writeOfficeReport(officeChildReport{ErrorCode: CodeConversionFailed})
		}
		if len(extracted.HTML) > MaxStaticPreviewBytes {
			return writeOfficeReport(officeChildReport{ErrorCode: CodeConversionExhausted})
		}
		return writeOfficeReport(officeChildReport{
			OK:           true,
			PageCount:    extracted.PageCount,
			SheetNames:   extracted.SheetNames,
			SlideCount:   extracted.SlideCount,
			Text:         extracted.Text,
			HTML:         string(extracted.HTML),
			AllowNetwork: false,
		})
	}
}

func officeAllocate() int {
	var blocks [][]byte
	sink := 0
	for i := 0; i < 64; i++ {
		block := make([]byte, 8<<20)
		for j := range block {
			block[j] = byte(i)
		}
		blocks = append(blocks, block)
		sink += int(block[0])
	}
	if sink < 0 {
		return 1
	}
	return writeOfficeReport(officeChildReport{OK: true, Text: "allocated"})
}

func writeOfficeReport(rep officeChildReport) int {
	rep.AllowNetwork = false
	buf, err := json.Marshal(rep)
	if err != nil {
		return 1
	}
	_, _ = os.Stdout.Write(append(buf, '\n'))
	return 0
}

func applyOfficeRlimits() {
	if n, ok := officeUintEnv(envOfficeCPUSeconds); ok && n > 0 {
		_ = syscall.Setrlimit(syscall.RLIMIT_CPU, &syscall.Rlimit{Cur: n, Max: n})
	}
	if n, ok := officeUintEnv(envOfficeASBytes); ok && n > 0 {
		_ = syscall.Setrlimit(syscall.RLIMIT_AS, &syscall.Rlimit{Cur: n, Max: n})
	}
}

func officeUintEnv(key string) (uint64, bool) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func officeEnvKeys() []string {
	keys := make([]string, 0, 8)
	for _, kv := range os.Environ() {
		key, _, ok := strings.Cut(kv, "=")
		if ok && key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func normalizeOfficeLimits(l OfficeLimits) OfficeLimits {
	if l.Mode == "" {
		l.Mode = "convert"
	}
	if l.Wall <= 0 {
		l.Wall = DefaultOfficeWall
	}
	if l.Mode == "convert" {
		if l.ASBytes == 0 {
			l.ASBytes = DefaultOfficeASBytes
		}
		if l.CPUSeconds == 0 {
			l.CPUSeconds = DefaultOfficeCPUSeconds
		}
	}
	return l
}

// officeChildEnv keeps PATH and TMPDIR. Every other parent variable is dropped,
// including database URLs, cookies, authorization, and proxy settings.
func officeChildEnv(parent []string, limits OfficeLimits, inputPath string) []string {
	allow := map[string]struct{}{
		"PATH":   {},
		"TMPDIR": {},
	}
	out := make([]string, 0, 8)
	for _, kv := range parent {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if _, keep := allow[key]; keep {
			out = append(out, kv)
		}
	}
	out = append(out,
		envOfficeChild+"=1",
		envOfficeMode+"="+limits.Mode,
		envOfficeInput+"="+inputPath,
		envOfficeFormat+"="+limits.Format,
		envOfficeAllowNetwork+"=0",
	)
	if limits.ASBytes > 0 {
		out = append(out, envOfficeASBytes+"="+strconv.FormatUint(limits.ASBytes, 10))
	}
	if limits.CPUSeconds > 0 {
		out = append(out, envOfficeCPUSeconds+"="+strconv.FormatUint(limits.CPUSeconds, 10))
	}
	return out
}

// ConvertIsolated runs the converter in a child process. Oversize input does
// not spawn. The child environment comes from officeChildEnv.
func ConvertIsolated(ctx context.Context, input []byte, limits OfficeLimits) (OfficeResult, error) {
	limits = normalizeOfficeLimits(limits)
	if limits.Mode == "convert" && int64(len(input)) > MaxOfficeInputBytes {
		return OfficeResult{ErrorCode: CodeConversionExhausted}, nil
	}
	inputPath := ""
	if limits.Mode == "convert" {
		f, err := os.CreateTemp("", "office-src-*")
		if err != nil {
			return OfficeResult{ErrorCode: CodeConversionFailed}, nil
		}
		inputPath = f.Name()
		defer os.Remove(inputPath)
		if _, err := f.Write(input); err != nil {
			f.Close()
			return OfficeResult{ErrorCode: CodeConversionFailed}, nil
		}
		if err := f.Close(); err != nil {
			return OfficeResult{ErrorCode: CodeConversionFailed}, nil
		}
	}
	bin, err := os.Executable()
	if err != nil {
		return OfficeResult{ErrorCode: CodeConversionFailed}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, limits.Wall)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	cmd.Env = officeChildEnv(os.Environ(), limits, inputPath)
	cmd.Stdin = nil
	cmd.Stderr = io.Discard
	var stdout capBuffer
	stdout.max = officeStdoutCap
	cmd.Stdout = &stdout
	runErr := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return OfficeResult{ErrorCode: CodeConversionTimeout}, nil
	}
	var rep officeChildReport
	if err := json.Unmarshal(bytes.TrimSpace(stdout.buf.Bytes()), &rep); err == nil && (rep.OK || rep.ErrorCode != "") {
		return OfficeResult{
			OK:           rep.OK,
			ErrorCode:    rep.ErrorCode,
			PageCount:    rep.PageCount,
			SheetNames:   rep.SheetNames,
			SlideCount:   rep.SlideCount,
			Text:         rep.Text,
			HTML:         []byte(rep.HTML),
			EnvKeys:      rep.EnvKeys,
			AllowNetwork: false,
		}, nil
	}
	if runErr != nil {
		return OfficeResult{ErrorCode: CodeConversionExhausted}, nil
	}
	return OfficeResult{ErrorCode: CodeConversionFailed}, nil
}

type capBuffer struct {
	buf bytes.Buffer
	n   int
	max int
}

func (c *capBuffer) Write(p []byte) (int, error) {
	if c.max > 0 && c.n >= c.max {
		return len(p), nil
	}
	write := p
	if c.max > 0 && c.n+len(write) > c.max {
		write = write[:c.max-c.n]
	}
	n, err := c.buf.Write(write)
	c.n += n
	if err != nil {
		return n, err
	}
	return len(p), nil
}
