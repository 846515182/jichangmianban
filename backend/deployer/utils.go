package deployer

import (
	"encoding/base64"
	"os"
	"os/exec"
)

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func execLocal(cmd string) (string, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	out, err := exec.Command(shell, "-c", cmd).CombinedOutput()
	return string(out), err
}
