package cli

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func writeYAML(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o770|os.ModeSetgid); err != nil {
		return err
	}
	data, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o640)
}
