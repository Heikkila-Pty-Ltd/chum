package temporal

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"
)

// hashFile computes the SHA-256 hex digest of a file.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// compareOutputs performs a normalized comparison of LLM output vs script output.
// Trims whitespace and normalizes line endings for tolerance of cosmetic differences.
func compareOutputs(llmOutput, scriptOutput string) bool {
	normalize := func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.ReplaceAll(s, "\r\n", "\n")
		s = strings.ReplaceAll(s, "\r", "\n")
		return s
	}
	return normalize(llmOutput) == normalize(scriptOutput)
}
