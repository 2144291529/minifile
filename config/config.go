package config

import (
	"gopkg.in/yaml.v3"
	"log"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Port        int    `yaml:"port"`
	ServerUrl   string `yaml:"server_url"`
	AllowDelete string `yaml:"allow_delete"`
}

func (c *Config) DeleteEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(c.AllowDelete), "on")
}

var GlobalConfig *Config

func ParseConfig(filename string) *Config {
	configPath, err := resolveConfigPath(filename)
	if err != nil {
		log.Fatalf("\n\nUnable to resolve config path: %+v\n\n", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("\n\nNot found config file, %+v\n\n", err)
	}

	var cfg *Config
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		log.Fatalf("\n\nUnable to parse config file, %+v\n\n", err)
	}
	GlobalConfig = cfg
	return cfg
}

func resolveConfigPath(filename string) (string, error) {
	if filename == "" {
		filename = "config.yaml"
	}

	if filepath.IsAbs(filename) {
		return filename, nil
	}

	if _, err := os.Stat(filename); err == nil {
		absPath, absErr := filepath.Abs(filename)
		if absErr == nil {
			return absPath, nil
		}
		return filename, nil
	}

	executable, err := os.Executable()
	if err != nil {
		return "", err
	}

	return filepath.Join(filepath.Dir(executable), filename), nil
}
