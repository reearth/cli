// Package iostreams abstracts stdin/stdout/stderr and decides how the CLI
// should present itself: human (TTY, colors, spinners, prompts) or machine
// (plain, no interaction).
package iostreams

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"golang.org/x/term"

	"github.com/reearth/cli/sdk/envvar"
)

type IOStreams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer

	stdinTTY  bool
	stdoutTTY bool
	stderrTTY bool

	outColor bool
	errColor bool

	// Agent is the name of the detected coding agent, or "" if none.
	Agent string
	// CI reports whether we run on a CI service.
	CI bool

	NoInput bool
	Quiet   bool

	progressMu sync.Mutex
	progress   *spinner
}

// System returns IOStreams bound to the process's standard streams.
func System() *IOStreams {
	s := &IOStreams{
		In:     os.Stdin,
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}
	s.stdinTTY = isTerminal(os.Stdin)
	s.stdoutTTY = isTerminal(os.Stdout)
	s.stderrTTY = isTerminal(os.Stderr)
	s.Agent = DetectAgent()
	s.CI = DetectCI()
	s.NoInput = envvar.True("REEARTH_NO_INPUT")
	s.outColor = colorEnabled(s.stdoutTTY)
	s.errColor = colorEnabled(s.stderrTTY)
	return s
}

// Test returns IOStreams backed by buffers. All streams are non-TTY.
func Test() (s *IOStreams, in *bytes.Buffer, out *bytes.Buffer, errOut *bytes.Buffer) {
	in, out, errOut = &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{}
	s = &IOStreams{In: in, Out: out, ErrOut: errOut}
	return
}

func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

func (s *IOStreams) IsStdinTTY() bool  { return s.stdinTTY }
func (s *IOStreams) IsStdoutTTY() bool { return s.stdoutTTY }
func (s *IOStreams) IsStderrTTY() bool { return s.stderrTTY }

// SetTTY overrides TTY detection (for tests).
func (s *IOStreams) SetTTY(stdin, stdout, stderr bool) {
	s.stdinTTY, s.stdoutTTY, s.stderrTTY = stdin, stdout, stderr
}

// SetColorEnabled forces colors on or off for both stdout and stderr.
func (s *IOStreams) SetColorEnabled(v bool) {
	s.outColor, s.errColor = v, v
}

// IsInteractive reports whether a human is likely watching stderr.
// Spinners, hints and update notices are shown only in this case.
func (s *IOStreams) IsInteractive() bool {
	return s.stderrTTY && s.Agent == "" && !s.CI
}

// CanPrompt reports whether it is OK to ask the user for input.
func (s *IOStreams) CanPrompt() bool {
	return !s.NoInput && s.stdinTTY && s.IsInteractive()
}

// Color returns the color scheme for stdout.
func (s *IOStreams) Color() *ColorScheme { return &ColorScheme{enabled: s.outColor} }

// ErrColor returns the color scheme for stderr.
func (s *IOStreams) ErrColor() *ColorScheme { return &ColorScheme{enabled: s.errColor} }

// TerminalWidth returns the width of stdout, or 80 if unknown.
func (s *IOStreams) TerminalWidth() int {
	if f, ok := s.Out.(*os.File); ok && s.stdoutTTY {
		if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
			return w
		}
	}
	return 80
}

// indent is the left margin for human-facing messages on a TTY.
func (s *IOStreams) indent() string {
	if s.stderrTTY {
		return "  "
	}
	return ""
}

func (s *IOStreams) message(icon, format string, args ...any) {
	if s.Quiet {
		return
	}
	s.StopProgress()
	msg := fmt.Sprintf(format, args...)
	if icon != "" {
		msg = icon + " " + msg
	}
	_, _ = fmt.Fprintln(s.ErrOut, s.indent()+msg)
}

// Success prints "✓ message" to stderr.
func (s *IOStreams) Success(format string, args ...any) {
	s.message(s.ErrColor().SuccessIcon(), format, args...)
}

// Warn prints "! message" to stderr. Warnings are printed even with --quiet.
func (s *IOStreams) Warn(format string, args ...any) {
	s.StopProgress()
	_, _ = fmt.Fprintln(s.ErrOut, s.indent()+s.ErrColor().WarningIcon()+" "+fmt.Sprintf(format, args...))
}

// Info prints "→ message" to stderr.
func (s *IOStreams) Info(format string, args ...any) {
	s.message(s.ErrColor().Arrow(), format, args...)
}

// Hint prints a dimmed secondary line to stderr, aligned under the previous message.
func (s *IOStreams) Hint(format string, args ...any) {
	if s.Quiet {
		return
	}
	pad := ""
	if s.stderrTTY {
		pad = "  "
	}
	_, _ = fmt.Fprintln(s.ErrOut, s.indent()+pad+s.ErrColor().Dim(fmt.Sprintf(format, args...)))
}

// Println prints a plain line to stderr with the standard margin.
func (s *IOStreams) Println(a ...any) {
	if s.Quiet {
		return
	}
	s.StopProgress()
	_, _ = fmt.Fprintln(s.ErrOut, s.indent()+strings.TrimSuffix(fmt.Sprint(a...), "\n"))
}

// Newline prints an empty line to stderr on a TTY (for visual rhythm only).
func (s *IOStreams) Newline() {
	if s.stderrTTY && !s.Quiet {
		_, _ = fmt.Fprintln(s.ErrOut)
	}
}

// StartProgress shows a spinner on stderr. It is a no-op when not interactive.
func (s *IOStreams) StartProgress(label string) {
	if !s.IsInteractive() || s.Quiet {
		return
	}
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	if s.progress != nil {
		s.progress.setLabel(label)
		return
	}
	s.progress = startSpinner(s.ErrOut, s.indent(), label, s.ErrColor())
}

// StopProgress removes the spinner if shown.
func (s *IOStreams) StopProgress() {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	if s.progress != nil {
		s.progress.stop()
		s.progress = nil
	}
}

// colorEnabled follows the conventions of these variables rather than
// envvar.Bool: any NO_COLOR disables color (no-color.org), and any
// CLICOLOR_FORCE but "0" forces it (bixense.com/clicolors).
func colorEnabled(tty bool) bool {
	if envvar.Get("NO_COLOR") != "" {
		return false
	}
	if v := envvar.Get("CLICOLOR_FORCE"); v != "" && v != "0" {
		return true
	}
	if envvar.Get("TERM") == "dumb" {
		return false
	}
	return tty
}
