package config

import (
	"bufio"
	"bytes"
	"os"
	"strings"
)

// LoadDotEnv reads a .env file into a map. A missing file is not an error —
// the process environment is always the authority.
func LoadDotEnv(path string) map[string]string {
	if path == "" {
		return nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseDotEnv(content)
}

func parseDotEnv(content []byte) map[string]string {
	out := make(map[string]string)
	sc := bufio.NewScanner(bytes.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if key != "" {
			out[key] = val
		}
	}
	return out
}

// Getenv returns a lookup that prefers the process environment and falls back
// to the parsed .env file.
func Getenv(file map[string]string) func(string) string {
	return func(key string) string {
		if v, ok := os.LookupEnv(key); ok && v != "" {
			return v
		}
		return file[key]
	}
}
