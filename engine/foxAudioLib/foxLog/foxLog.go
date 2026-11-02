package foxLog

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Logger struct {
	mu           sync.Mutex
	LogFile      *os.File
	DebugEnabled bool
	ClientId     string
}

const (
	Info       = "Info"
	Debug      = "Debug"
	Error      = "Error"
	Warn       = "Warn"
	FatalError = "FatalError"
)

func NewLogger(logFilePath, clientId string, debugEnabled bool) (*Logger, error) {
	logFile, err := os.OpenFile(
		filepath.Clean(logFilePath),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY,
		0644,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}

	if clientId == "" {
		clientId = "xx:xx:xx:xx:xx:xx"
	}

	return &Logger{
		LogFile:      logFile,
		ClientId:     clientId,
		DebugEnabled: debugEnabled,
	}, nil
}

func (l *Logger) Log(logType, description string) {
	if logType == Debug && !l.DebugEnabled {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	logEntry := ""
	// Info log type needs to be consistent
	if logType == "Info" {
		logEntry = fmt.Sprintf("%s %s %s\n",
			time.Now().Format("2006-01-02 15:04:05.999999"),
			l.ClientId,
			description,
		)
	} else {
		logEntry = fmt.Sprintf("%s %s [%s] %s\n",
			time.Now().Format("2006-01-02 15:04:05.999999"),
			l.ClientId,
			logType,
			description,
		)
	}
	if _, err := l.LogFile.WriteString(logEntry); err != nil {
		log.Printf("LOG ERROR: Failed to write log entry: %v", err)
	}
}

// Simplified helper methods
func (l *Logger) Debug(description string) { l.Log(Debug, description) }
func (l *Logger) Info(description string)  { l.Log(Info, description) }
func (l *Logger) Warn(description string)  { l.Log(Warn, description) }
func (l *Logger) Error(description string) { l.Log(Error, description) }

func (l *Logger) FatalError(description string) {
	l.Log(FatalError, description)
	l.Close()
	os.Exit(1)
}

func (l *Logger) flush() {

	if l.LogFile != nil {
		if err := l.LogFile.Sync(); err != nil {
			log.Printf("LOG ERROR: Failed to sync log file: %v", err)
		}
	}
}

func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.LogFile != nil {
		l.flush()
		if err := l.LogFile.Close(); err != nil {
			log.Printf("LOG ERROR: Failed to close log file: %v", err)
		}
		l.LogFile = nil
	}
}
