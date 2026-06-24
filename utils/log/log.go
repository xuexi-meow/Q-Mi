package log

import (
	"context"
	"io"
	"log/slog"
	"strings"
)

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorPurple = "\033[35m"
	colorCyan   = "\033[36m"
)

type CustomHandler struct {
	writer io.Writer
	level  slog.Leveler
}

// WithAttrs implements [slog.Handler].
func (h *CustomHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	panic("unimplemented")
}

// WithGroup implements [slog.Handler].
func (h *CustomHandler) WithGroup(name string) slog.Handler {
	panic("unimplemented")
}

func NewCustomLogHandler(writer io.Writer, level slog.Leveler) *CustomHandler {
	return &CustomHandler{
		writer: writer,
		level:  level,
	}
}

func (h *CustomHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *CustomHandler) Handle(ctx context.Context, record slog.Record) error {
	var buf strings.Builder
	switch record.Level {
	case slog.LevelDebug:
		buf.WriteString(colorBlue)
		buf.WriteString("[")
		buf.WriteString(record.Time.Format("2006-01-02 15:04:05"))
		buf.WriteString("] [DEBUG] ")
		buf.WriteString(record.Message)
		buf.WriteString(colorReset)
	case slog.LevelInfo:
		buf.WriteString("[")
		buf.WriteString(colorGreen)
		buf.WriteString(record.Time.Format("2006-01-02 15:04:05"))
		buf.WriteString(colorReset)
		buf.WriteString("] [")
		buf.WriteString(colorGreen)
		buf.WriteString("INFO")
		buf.WriteString(colorReset)
		buf.WriteString("] ")
		buf.WriteString("\033[1m")
		buf.WriteString(record.Message)
		buf.WriteString("\033[0m")
	case slog.LevelWarn:
		buf.WriteString("[")
		buf.WriteString(colorYellow)
		buf.WriteString(record.Time.Format("2006-01-02 15:04:05"))
		buf.WriteString(colorReset)
		buf.WriteString("] [")
		buf.WriteString(colorYellow)
		buf.WriteString("WARN")
		buf.WriteString(colorReset)
		buf.WriteString("] ")
		buf.WriteString(colorYellow)
		buf.WriteString("\033[1m")
		buf.WriteString(record.Message)
		buf.WriteString("\033[0m")
		buf.WriteString(colorReset)
	case slog.LevelError:
		buf.WriteString("[")
		buf.WriteString(colorRed)
		buf.WriteString(record.Time.Format("2006-01-02 15:04:05"))
		buf.WriteString(colorReset)
		buf.WriteString("] [")
		buf.WriteString(colorRed)
		buf.WriteString("ERROR")
		buf.WriteString(colorReset)
		buf.WriteString("] ")
		buf.WriteString(colorRed)
		buf.WriteString("\033[1m")
		buf.WriteString(record.Message)
		buf.WriteString("\033[0m")
		buf.WriteString(colorReset)
	default:
		buf.WriteString("[")
		buf.WriteString(record.Time.Format("2006-01-02 15:04:05"))
		buf.WriteString("] [")
		buf.WriteString(levelAbbr(record.Level))
		buf.WriteString("] ")
		buf.WriteString(record.Message)
	}

	record.Attrs(func(attr slog.Attr) bool {
		buf.WriteString(" ")
		buf.WriteString(colorCyan)
		buf.WriteString(attr.Key)
		buf.WriteString("=")
		buf.WriteString(attr.Value.String())
		buf.WriteString(colorReset)
		return true
	})

	buf.WriteString("\n")
	_, err := h.writer.Write([]byte(buf.String()))
	return err
}

func levelAbbr(level slog.Level) string {
	switch level {
	case slog.LevelDebug:
		return "DEBUG"
	case slog.LevelInfo:
		return "INFO"
	case slog.LevelWarn:
		return "WARN"
	case slog.LevelError:
		return "ERROR"
	default:
		return level.String()
	}
}
