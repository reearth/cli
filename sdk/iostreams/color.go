package iostreams

import "fmt"

// ColorScheme renders styled text. When disabled, it returns text unchanged
// (icons are kept so meaning survives NO_COLOR).
type ColorScheme struct {
	enabled bool
}

func (c *ColorScheme) Enabled() bool { return c.enabled }

func (c *ColorScheme) wrap(code, s string) string {
	if !c.enabled || s == "" {
		return s
	}
	return fmt.Sprintf("\x1b[%sm%s\x1b[0m", code, s)
}

func (c *ColorScheme) Bold(s string) string   { return c.wrap("1", s) }
func (c *ColorScheme) Dim(s string) string    { return c.wrap("2", s) }
func (c *ColorScheme) Red(s string) string    { return c.wrap("31", s) }
func (c *ColorScheme) Green(s string) string  { return c.wrap("32", s) }
func (c *ColorScheme) Yellow(s string) string { return c.wrap("33", s) }
func (c *ColorScheme) Cyan(s string) string   { return c.wrap("36", s) }

// Accent is the single brand color used for emphasis.
func (c *ColorScheme) Accent(s string) string { return c.wrap("38;5;43", s) }

func (c *ColorScheme) SuccessIcon() string { return c.Green("✓") }
func (c *ColorScheme) FailureIcon() string { return c.Red("✗") }
func (c *ColorScheme) WarningIcon() string { return c.Yellow("!") }
func (c *ColorScheme) Arrow() string       { return c.Accent("→") }
func (c *ColorScheme) Question() string    { return c.Accent("?") }

// ActiveMark marks the active item in a list (● / blank).
func (c *ColorScheme) ActiveMark(active bool) string {
	if active {
		return c.Accent("●")
	}
	return " "
}
