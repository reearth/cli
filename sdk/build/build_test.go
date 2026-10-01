package build

import "testing"

func TestIsDev(t *testing.T) {
	cases := map[string]bool{
		"dev":                               true,
		"":                                  true,
		"(devel)":                           true,
		"0.1.1-SNAPSHOT-72d53b5":            true,
		"0.0.0-20261001183638-72d53b549939": true,
		"0.0.0-20261001183638-72d53b549939+dirty": true,
		"1.2.4-0.20261001183638-72d53b549939":     true,
		"1.2.3+dirty":                             true,
		"1.2.3":                                   false,
		"v1.2.3":                                  false,
		"1.3.0-rc.1":                              false,
	}
	for v, want := range cases {
		if got := isDev(v); got != want {
			t.Errorf("isDev(%q) = %v, want %v", v, got, want)
		}
	}
}
