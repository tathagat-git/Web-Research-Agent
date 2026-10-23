package main

import (
	"bufio"
	"os"
	"strings"
)

// loadDotEnv reads KEY=VALUE lines from a file (normally ".env") and sets them
// as environment variables. Rules:
//   - a missing file is not an error
//   - blank lines and lines starting with # are ignored
//   - variables that are already set in the real environment are NOT overridden
//   - inline comments are not supported; wrap values in quotes if needed
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = unquote(strings.TrimSpace(value))

		if key != "" && os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
	return sc.Err()
}

// unquote removes one pair of matching surrounding quotes.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
