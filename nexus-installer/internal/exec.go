package internal

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// RunCommand executes a shell command and returns its combined output.
// It also appends the command and output to /var/log/nexus-install.log.
func RunCommand(command string) (string, error) {
	cmd := exec.Command("sh", "-c", command)
	output, err := cmd.CombinedOutput()
	outStr := string(output)

	// Log to file
	logFile := "/var/log/nexus-install.log"
	
	// Prepare log entry
	logEntry := fmt.Sprintf("\n--- [%s] ---\nCommand: %s\nOutput:\n%s\n", 
		os.Getenv("USER"), command, outStr)
	if err != nil {
		logEntry += fmt.Sprintf("Error: %v\n", err)
	}

	// Use sudo tee to append to the log file since the installer might not have direct write access
	// We'll escape the entry for the shell
	escapedEntry := strings.ReplaceAll(logEntry, "'", "'\\''")
	logCmd := exec.Command("sh", "-c", fmt.Sprintf("echo '%s' | sudo tee -a %s > /dev/null", escapedEntry, logFile))
	logCmd.Run()

	return outStr, err
}
