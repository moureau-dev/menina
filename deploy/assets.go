package deployassets

import (
	"embed"
	"fmt"
	"path"
	"strings"
)

//go:embed systemd/*.service zot/*
var FS embed.FS

func SystemdUnit(name string) (string, error) {
	if !strings.HasSuffix(name, ".service") {
		name += ".service"
	}
	data, err := FS.ReadFile(path.Join("systemd", name))
	if err != nil {
		return "", fmt.Errorf("systemd unit %q not found: %w", name, err)
	}
	return string(data), nil
}

func RenderUnit(name string, vars map[string]string) (string, error) {
	content, err := SystemdUnit(name)
	if err != nil {
		return "", err
	}
	for k, v := range vars {
		content = strings.ReplaceAll(content, "@"+k+"@", v)
	}
	return content, nil
}

func ZotDefaultConfig() (string, error) {
	data, err := FS.ReadFile("zot/config.json")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
