// Package envvar is the one way the CLI reads environment variables. Every
// variable the CLI reads goes through Get, Lookup, Bool or True, so tests can
// replace the whole environment with a map (see envvartest.Fake). The one
// exception is passing the whole environment to a child process
// (os.Environ), which is not a read.
package envvar

import (
	"os"
	"strings"
	"sync/atomic"
)

// Map is an environment given as key/value pairs.
type Map map[string]string

// lookup is atomic so that goroutines a test leaves running do not race
// with the restore of a fake.
var lookup atomic.Pointer[func(string) (string, bool)]

func init() {
	l := os.LookupEnv
	lookup.Store(&l)
}

// Lookup returns the value of key and whether it is set.
func Lookup(key string) (string, bool) { return (*lookup.Load())(key) }

// Get returns the value of key, or "" if it is not set.
func Get(key string) string {
	v, _ := Lookup(key)
	return v
}

// Use replaces the environment the package reads until restore is called.
func Use(l func(string) (string, bool)) (restore func()) {
	prev := lookup.Swap(&l)
	return func() { lookup.Store(prev) }
}

// Bool reads key as a boolean: 1/true/yes/on or 0/false/no/off,
// case-insensitively. ok is false for any other value, including unset or "".
func Bool(key string) (value, ok bool) {
	switch strings.ToLower(strings.TrimSpace(Get(key))) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	return false, false
}

// True reports whether key holds a true boolean value.
func True(key string) bool {
	b, ok := Bool(key)
	return ok && b
}
