package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const DefaultConfigPath = "/opt/wast/config.json"

type Config struct {
	Domain     string `json:"domain"`
	PublicKey  string `json:"public_key"`
	ShortID    string `json:"short_id"`
	InboundTag string `json:"inbound_tag"`
	APIAddr    string `json:"api_addr"`
	XrayBin    string `json:"xray_bin"`
	DBPath     string `json:"db_path"`
	SubDir     string `json:"sub_dir"`
	Country    string `json:"country"`
	SubBaseURL string `json:"sub_base_url"`
	configPath string `json:"-"`
}

func DefaultConfig() *Config {
	return &Config{
		InboundTag: "vless-in",
		APIAddr:    "127.0.0.1:10085",
		XrayBin:    "/usr/local/bin/xray",
		DBPath:     "/opt/wast/wast.db",
		SubDir:     "/var/www/wast/sub",
		Country:    "Default",
		configPath: DefaultConfigPath,
	}
}

func LoadConfig(customPath ...string) (*Config, error) {
	cfg := DefaultConfig()
	p := DefaultConfigPath
	if env := os.Getenv("WAST_CONFIG"); env != "" {
		p = env
	}
	if len(customPath) > 0 && customPath[0] != "" {
		p = customPath[0]
	}
	cfg.configPath = p

	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	if cfg.SubBaseURL == "" && cfg.Domain != "" {
		cfg.SubBaseURL = "https://" + cfg.Domain + "/sub"
	}
	if cfg.Country == "" {
		cfg.Country = "Default"
	}

	return cfg, nil
}

func (c *Config) Save() error {
	if err := os.MkdirAll(filepath.Dir(c.configPath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.configPath, data, 0644)
}
