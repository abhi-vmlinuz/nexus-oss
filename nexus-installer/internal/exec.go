package internal

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

var GlobalOutputWriter io.Writer

// RunCommand executes a shell command. It uses GlobalOutputWriter for real-time streaming if set.
func RunCommand(command string) (string, error) {
	cmd := exec.Command("sh", "-c", command)
	
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		return "", err
	}

	var fullOutput strings.Builder
	multi := io.MultiWriter(&fullOutput, os.Stdout) // Also log to stdout for debugging
	if GlobalOutputWriter != nil {
		multi = io.MultiWriter(multi, GlobalOutputWriter)
	}

	// Stream stdout and stderr
	go io.Copy(multi, stdout)
	go io.Copy(multi, stderr)

	err := cmd.Wait()
	outStr := fullOutput.String()

	// Log to persistent file
	logFile := "/var/log/nexus-install.log"
	logEntry := fmt.Sprintf("\n--- [%s] ---\nCommand: %s\nOutput:\n%s\n", 
		os.Getenv("USER"), command, outStr)
	if err != nil {
		logEntry += fmt.Sprintf("Error: %v\n", err)
	}

	escapedEntry := strings.ReplaceAll(logEntry, "'", "'\\''")
	exec.Command("sh", "-c", fmt.Sprintf("echo '%s' | sudo tee -a %s > /dev/null", escapedEntry, logFile)).Run()

	return outStr, err
}
