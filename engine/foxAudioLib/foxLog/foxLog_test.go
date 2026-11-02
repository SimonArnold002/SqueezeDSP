package foxLog_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Foxenfurter/foxAudioLib/foxLog"
)

func TestLoggerDebugTrue(t *testing.T) {
	fmt.Println("Running Logging Test: TestLoggerDebugTrue")

	logFileDir := "C:/temp/gotesting/output"
	//logFileDir := "C:/temp"
	// Create directories if they do not exist
	err := os.MkdirAll((logFileDir), os.ModePerm)
	if err != nil {
		fmt.Println("Error creating directories:", err)
		return
	}

	DebugEnabled := true

	// Create a logger instance for testing
	logFilePath := filepath.Join(logFileDir, "logtest.txt")

	// Clean up (delete test log file)
	os.Remove(logFilePath)

	logger, err := foxLog.NewLogger(logFilePath, "", DebugEnabled)
	if err != nil {
		t.Fatalf("Error creating logger: %v", err)
	}
	defer logger.Close()

	// Log some entries
	logger.Info("Test normal log message.")
	logger.Debug("Test debug log message.")
	logger.Error("Test error log message.")
	logger.Warn("Test warn log message.")

	// Check if log file is created and contains expected content
	content, err := os.ReadFile(logFilePath)
	if err != nil {
		t.Fatalf("Error reading log file: %v", err)
	}

	expectedLines := []string{
		"[Info] Test normal log message.",
		"[Debug] Test debug log message.",
		"[Error] Test error log message.",
		"[Warn] Test warn log message.",
	}

	// split the lines
	actualLines := strings.Split(string(content), "\n")
	// check the line ending is present in the actual lines
	for i, expectedLine := range expectedLines {
		if !strings.HasSuffix(actualLines[i], expectedLine) {
			t.Errorf("Unexpected log file content. Expected line ending with:\n%s\nGot:\n%s", expectedLine, actualLines[i])
		}
	}

}

func TestLoggerDebugFalse(t *testing.T) {
	fmt.Println("Running Logging Test: TestLoggerDebugFalse")

	logFileDir := "C:/temp/gotesting/output"
	//logFileDir := "C:/temp"
	// Create directories if they do not exist
	err := os.MkdirAll((logFileDir), os.ModePerm)
	if err != nil {
		fmt.Println("Error creating directories:", err)
		return
	}

	DebugEnabled := false

	// Create a logger instance for testing
	logFilePath := filepath.Join(logFileDir, "logtest.txt")

	// Clean up (delete test log file)
	os.Remove(logFilePath)

	logger, err := foxLog.NewLogger(logFilePath, "", DebugEnabled)
	if err != nil {
		t.Fatalf("Error creating logger: %v", err)
	}
	defer logger.Close()

	// Log some entries
	logger.Info("Test normal log message.")
	logger.Debug("Test debug log message.")
	logger.Error("Test error log message.")
	logger.Warn("Test warn log message.")

	// Check if log file is created and contains expected content
	content, err := os.ReadFile(logFilePath)
	if err != nil {
		t.Fatalf("Error reading log file: %v", err)
	}

	expectedLines := []string{
		"[Info] Test normal log message.",
		"[Error] Test error log message.",
		"[Warn] Test warn log message.",
	}

	// split the lines
	actualLines := strings.Split(string(content), "\n")
	// check the line ending is present in the actual lines
	for i, expectedLine := range expectedLines {
		if !strings.HasSuffix(actualLines[i], expectedLine) {
			t.Errorf("Unexpected log file content. Expected line ending with:\n%s\nGot:\n%s", expectedLine, actualLines[i])
		}
	}
}

func TestMain(m *testing.M) {
	// Run tests
	fmt.Println("Running Logging Test:")
	exitCode := m.Run()

	// Exit with the test result code
	os.Exit(exitCode)
}
