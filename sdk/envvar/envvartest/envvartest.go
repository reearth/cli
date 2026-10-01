// Package envvartest replaces the environment envvar reads in tests.
package envvartest

import (
	"testing"

	"github.com/reearth/cli/sdk/envvar"
)

// Fake makes envvar read vars instead of the process environment until the
// test ends, and returns the live map so the test can set more keys. Variables
// not in the map are unset, whatever the real environment holds. Like
// t.Setenv, it must not be used in parallel tests.
func Fake(t testing.TB, vars envvar.Map) envvar.Map {
	t.Helper()
	if vars == nil {
		vars = envvar.Map{}
	}
	restore := envvar.Use(func(k string) (string, bool) {
		v, ok := vars[k]
		return v, ok
	})
	t.Cleanup(restore)
	return vars
}
