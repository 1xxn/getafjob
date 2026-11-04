package config

import "log/slog"

// redacted replaces secrets in every output.
const redacted = "[redacted]"

// Secret is a secret (password, key, connection URL with password). fmt, slog and encoding/json print
// only "[redacted]"; the plain text is available through [Secret.Reveal] alone.
type Secret string

// Reveal returns the plain text. Call it only where the value is really needed.
func (s Secret) Reveal() string { return string(s) }

// String redacts the secret (fmt.Print, %v, %s).
func (s Secret) String() string { return redacted }

// GoString redacts the secret (%#v).
func (s Secret) GoString() string { return redacted }

// LogValue redacts the secret in slog.
func (s Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalText redacts the secret in JSON/YAML.
func (s Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }
