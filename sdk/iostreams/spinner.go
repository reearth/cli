package iostreams

import (
	"fmt"
	"io"
	"sync"
	"time"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type spinner struct {
	w      io.Writer
	prefix string
	cs     *ColorScheme

	mu    sync.Mutex
	label string

	done chan struct{}
	wg   sync.WaitGroup
}

func startSpinner(w io.Writer, prefix, label string, cs *ColorScheme) *spinner {
	s := &spinner{w: w, prefix: prefix, label: label, cs: cs, done: make(chan struct{})}
	s.wg.Add(1)
	go s.run()
	return s
}

func (s *spinner) setLabel(label string) {
	s.mu.Lock()
	s.label = label
	s.mu.Unlock()
}

func (s *spinner) run() {
	defer s.wg.Done()
	t := time.NewTicker(80 * time.Millisecond)
	defer t.Stop()
	for i := 0; ; i++ {
		s.mu.Lock()
		label := s.label
		s.mu.Unlock()
		_, _ = fmt.Fprintf(s.w, "\r\x1b[K%s%s %s", s.prefix, s.cs.Accent(spinnerFrames[i%len(spinnerFrames)]), s.cs.Dim(label))
		select {
		case <-s.done:
			_, _ = fmt.Fprint(s.w, "\r\x1b[K")
			return
		case <-t.C:
		}
	}
}

func (s *spinner) stop() {
	close(s.done)
	s.wg.Wait()
}
