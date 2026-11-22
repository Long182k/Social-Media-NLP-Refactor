package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// LoadDotEnvIfPresent reads key=value pairs from the first existing .env file
// in paths without overwriting environment variables that are already set.
func LoadDotEnvIfPresent(paths ...string) error {
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		f, err := os.Open(path) // #nosec G304 -- The application supplies known local config paths.
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("open env file %s: %w", path, err)
		}

		parseErr := parseAndApplyDotEnv(f)
		closeErr := f.Close()
		if parseErr != nil {
			return fmt.Errorf("parse env file %s: %w", path, parseErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close env file %s: %w", path, closeErr)
		}
		return nil
	}
	return nil
}

func parseAndApplyDotEnv(f *os.File) error {
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return fmt.Errorf("line %d: expected KEY=VALUE", lineNum)
		}
		key := strings.TrimSpace(line[:eq])
		if key == "" {
			return fmt.Errorf("line %d: empty key", lineNum)
		}
		val := strings.TrimSpace(line[eq+1:])
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			} else if idx := strings.Index(val, " #"); idx >= 0 {
				val = strings.TrimSpace(val[:idx])
			}
		} else if idx := strings.Index(val, " #"); idx >= 0 {
			val = strings.TrimSpace(val[:idx])
		}

		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, val); err != nil {
				return fmt.Errorf("set env %s: %w", key, err)
			}
		}
	}
	return scanner.Err()
}
