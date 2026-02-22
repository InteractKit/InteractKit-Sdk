package core

import (
	"fmt"
	"os"
	"time"
)

var loggerInstance Logger = *NewDevelopmentLogger() // default to development logger

// SetLogger sets the global logger instance
func SetLogger(logger Logger) {
	loggerInstance = logger
}

// GetLogger retrieves the global logger instance
func GetLogger() *Logger {
	return &loggerInstance
}

type Logger struct {
	handlerFunc func(level string, msg string, attrs map[string]interface{})
	attrs       map[string]interface{}
}

func NewLogger(handler func(level string, msg string, attrs map[string]interface{})) *Logger {
	return &Logger{
		handlerFunc: handler,
		attrs:       make(map[string]interface{}),
	}
}

// NewDevelopmentLogger creates a new development logger with pretty console output
func NewDevelopmentLogger() *Logger {
	handler := func(level string, msg string, attrs map[string]interface{}) {
		timestamp := time.Now().Format(time.RFC3339)
		attrStr := ""
		if len(attrs) > 0 {
			attrStr = " | "
			for k, v := range attrs {
				attrStr += fmt.Sprintf("%s=%v ", k, v)
			}
			attrStr = attrStr[:len(attrStr)-1] // remove trailing space
		}
		logLine := fmt.Sprintf("%s [%s] %s%s\n", timestamp, level, msg, attrStr)
		switch level {
		case "FATAL":
			fmt.Fprint(os.Stderr, logLine)
			os.Exit(1)
		case "PANIC":
			fmt.Fprint(os.Stderr, logLine)
			panic(msg)
		default:
			fmt.Print(logLine)
		}
	}

	return &Logger{
		handlerFunc: handler,
		attrs:       make(map[string]interface{}),
	}
}

func (l *Logger) log(level string, msg string, args ...interface{}) {
	if l.handlerFunc != nil {
		if len(args) > 0 {
			// Detect slog-style key-value pairs: even number of args where
			// odd-positioned args (keys) are strings.
			if isKeyValuePairs(args) {
				attrs := make(map[string]interface{}, len(l.attrs)+len(args)/2)
				for k, v := range l.attrs {
					attrs[k] = v
				}
				for i := 0; i < len(args)-1; i += 2 {
					key, _ := args[i].(string)
					attrs[key] = args[i+1]
				}
				l.handlerFunc(level, msg, attrs)
				return
			}
			msg = fmt.Sprintf(msg, args...)
		}
		l.handlerFunc(level, msg, l.attrs)
	}
}

// isKeyValuePairs returns true if args look like slog-style key-value pairs:
// even count and every key (even index) is a string.
func isKeyValuePairs(args []interface{}) bool {
	if len(args)%2 != 0 {
		return false
	}
	for i := 0; i < len(args); i += 2 {
		if _, ok := args[i].(string); !ok {
			return false
		}
	}
	return true
}

func (l *Logger) Debug(msg string, args ...interface{}) {
	l.log("DEBUG", msg, args...)
}

func (l *Logger) Debugf(format string, args ...interface{}) {
	l.log("DEBUG", format, args...)
}

func (l *Logger) Info(msg string, args ...interface{}) {
	l.log("INFO", msg, args...)
}

func (l *Logger) Infof(format string, args ...interface{}) {
	l.log("INFO", format, args...)
}

func (l *Logger) Warn(msg string, args ...interface{}) {
	l.log("WARN", msg, args...)
}

func (l *Logger) Warnf(format string, args ...interface{}) {
	l.log("WARN", format, args...)
}

func (l *Logger) Error(msg string, args ...interface{}) {
	l.log("ERROR", msg, args...)
}

func (l *Logger) Errorf(format string, args ...interface{}) {
	l.log("ERROR", format, args...)
}

func (l *Logger) Fatal(msg string, args ...interface{}) {
	l.log("FATAL", msg, args...)
}

func (l *Logger) Fatalf(format string, args ...interface{}) {
	l.log("FATAL", format, args...)
}

func (l *Logger) Panic(msg string, args ...interface{}) {
	l.log("PANIC", msg, args...)
}

func (l *Logger) Panicf(format string, args ...interface{}) {
	l.log("PANIC", format, args...)
}

func (l *Logger) Trace(msg string, args ...interface{}) {
	l.log("TRACE", msg, args...)
}

func (l *Logger) Tracef(format string, args ...interface{}) {
	l.log("TRACE", format, args...)
}

func (l *Logger) With(attrs map[string]interface{}) *Logger {
	combinedAttrs := make(map[string]interface{})
	for k, v := range l.attrs {
		combinedAttrs[k] = v
	}
	for k, v := range attrs {
		combinedAttrs[k] = v
	}
	return &Logger{
		handlerFunc: l.handlerFunc,
		attrs:       combinedAttrs,
	}
}

// Sync is a no-op for fmt-based logger
func (l *Logger) Sync() error {
	return nil
}
