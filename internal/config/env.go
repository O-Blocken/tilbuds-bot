package config

import (
	"os"
	"strings"
	"sync"
)

var dotenv = struct {
	once   sync.Once
	values map[string]string
}{
	values: map[string]string{},
}

func GetEnv(name string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}

	dotenv.once.Do(func() {
		values, err := loadDotenv(".env")
		if err == nil {
			dotenv.values = values
		}
	})
	return strings.TrimSpace(dotenv.values[name])
}

func loadDotenv(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}

	values := map[string]string{}
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		values[strings.TrimSpace(key)] = trimMatchingQuotes(strings.TrimSpace(value))
	}
	return values, nil
}

func trimMatchingQuotes(value string) string {
	if len(value) < 2 {
		return value
	}

	first := value[0]
	last := value[len(value)-1]
	if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
		return value[1 : len(value)-1]
	}
	return value
}
