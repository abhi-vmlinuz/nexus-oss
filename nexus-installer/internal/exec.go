package internal

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var GlobalOutputWriter io.Writer
var ansiRegex = regexp.MustCompile("[\u001b\u009b][[()#;?]*(?:[0-9]{1,4}(?:;[0-9]{0,4})*)?[0-9A-ORZcf-nqry=><]")

func StripANSI(str string) string {
	return ansiRegex.ReplaceAllString(str, "")
}

// RunCommand executes a shell command. It uses GlobalOutputWriter for real-time streaming if set.
func RunCommand(command string) (string, error) {
	cmd := exec.Command("sh", "-c", command)
	
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		return "", err
	}

	var fullOutput strings.Builder
	multi := io.Writer(&fullOutput)
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
