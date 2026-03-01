// CLAUDE:SUMMARY Configuration struct and defaults for the docpipe document extraction pipeline.
package docpipe

import "log/slog"

// Config configures the document pipeline.
type Config struct {
	// MaxFileSize is the maximum file size to process (default: 100 MB).
	MaxFileSize int64 `json:"max_file_size" yaml:"max_file_size"`

	// Logger for debug/error messages.
	Logger *slog.Logger `json:"-" yaml:"-"`
}

func (c *Config) defaults() {
	if c.MaxFileSize <= 0 {
		c.MaxFileSize = 100 * 1024 * 1024
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}
