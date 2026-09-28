// Package prompt provides minimal, line-based interactive prompts written to stderr.
package prompt

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/reearth/cli/sdk/iostreams"
)

// ErrNoInput is returned when a prompt is required but input is not possible.
var ErrNoInput = errors.New("input required but prompting is disabled")

// ErrCancelled is returned when the user closes stdin (Ctrl-D).
var ErrCancelled = errors.New("cancelled")

type Prompter interface {
	Input(label, def string) (string, error)
	Password(label string) (string, error)
	Confirm(label string, def bool) (bool, error)
	Select(label string, options []string, def int) (int, error)
}

// New returns a Prompter for the given streams. If prompting is not possible,
// every call returns ErrNoInput.
func New(io *iostreams.IOStreams) Prompter {
	return &linePrompter{io: io, r: bufio.NewReader(io.In)}
}

type linePrompter struct {
	io *iostreams.IOStreams
	r  *bufio.Reader
}

func (p *linePrompter) prefix() string {
	return "  " + p.io.ErrColor().Question() + " "
}

func (p *linePrompter) ask(label, hint string) (string, error) {
	if !p.io.CanPrompt() {
		return "", ErrNoInput
	}
	p.io.StopProgress()
	cs := p.io.ErrColor()
	line := p.prefix() + cs.Bold(label)
	if hint != "" {
		line += " " + cs.Dim(hint)
	}
	_, _ = fmt.Fprint(p.io.ErrOut, line+" "+cs.Dim("›")+" ")
	s, err := p.r.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			_, _ = fmt.Fprintln(p.io.ErrOut)
			return "", ErrCancelled
		}
		return "", err
	}
	return strings.TrimSpace(s), nil
}

func (p *linePrompter) Input(label, def string) (string, error) {
	hint := ""
	if def != "" {
		hint = "(" + def + ")"
	}
	s, err := p.ask(label, hint)
	if err != nil {
		return "", err
	}
	if s == "" {
		return def, nil
	}
	return s, nil
}

func (p *linePrompter) Password(label string) (string, error) {
	if !p.io.CanPrompt() {
		return "", ErrNoInput
	}
	f, ok := p.io.In.(*os.File)
	if !ok {
		return p.ask(label, "")
	}
	_, _ = fmt.Fprint(p.io.ErrOut, p.prefix()+p.io.ErrColor().Bold(label)+" "+p.io.ErrColor().Dim("›")+" ")
	b, err := term.ReadPassword(int(f.Fd()))
	_, _ = fmt.Fprintln(p.io.ErrOut)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func (p *linePrompter) Confirm(label string, def bool) (bool, error) {
	hint := "(y/N)"
	if def {
		hint = "(Y/n)"
	}
	for {
		s, err := p.ask(label, hint)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(s) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
	}
}

func (p *linePrompter) Select(label string, options []string, def int) (int, error) {
	if !p.io.CanPrompt() {
		return 0, ErrNoInput
	}
	cs := p.io.ErrColor()
	_, _ = fmt.Fprintln(p.io.ErrOut, p.prefix()+cs.Bold(label))
	for i, o := range options {
		mark := "  "
		if i == def {
			mark = cs.Accent("› ")
		}
		_, _ = fmt.Fprintf(p.io.ErrOut, "    %s%s %s\n", mark, cs.Dim(strconv.Itoa(i+1)+"."), o)
	}
	for {
		s, err := p.ask("Choose", fmt.Sprintf("(1-%d, default %d)", len(options), def+1))
		if err != nil {
			return 0, err
		}
		if s == "" {
			return def, nil
		}
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= len(options) {
			return n - 1, nil
		}
		for i, o := range options {
			if strings.EqualFold(o, s) {
				return i, nil
			}
		}
	}
}
