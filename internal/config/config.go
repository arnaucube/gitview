package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Repo struct {
		RefreshInterval time.Duration `mapstructure:"refreshInterval"`
		ScanPath        string        `mapstructure:"scanPath"`
		Pinned          []string      `mapstructure:"pinned,omitempty"`
		Readme          []string      `mapstructure:"readme"`
		MainBranch      []string      `mapstructure:"mainBranch"`
		Ignore          []string      `mapstructure:"ignore,omitempty"`
		Unlisted        []string      `mapstructure:"unlisted,omitempty"`
	} `mapstructure:"repo"`
	Dirs struct {
		Templates string `mapstructure:"templates"`
		Static    string `mapstructure:"static"`
	} `mapstructure:"dirs"`
	Meta struct {
		Title           string `mapstructure:"title"`
		Username        string `mapstructure:"username"`
		Description     string `mapstructure:"description"`
		SyntaxHighlight string `mapstructure:"syntaxHighlight"`
		Logo            string `mapstructure:"logo"`
		Favicon         string `mapstructure:"favicon"`

		FaviconHref string
		FaviconType string
	} `mapstructure:"meta"`
	Server struct {
		Name string `mapstructure:"name,omitempty"`
		Host string `mapstructure:"host"`
		Port int    `mapstructure:"port"`
	} `mapstructure:"server"`
}

func Read(f string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(f)
	v.SetDefault("repo.refreshInterval", "30s")
	v.SetEnvPrefix("GITVIEW")
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	c := &Config{}
	if err := v.Unmarshal(c); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if c.Repo.RefreshInterval <= 0 {
		return nil, fmt.Errorf("repo.refreshInterval must be positive")
	}

	var err error
	if c.Repo.ScanPath, err = filepath.Abs(c.Repo.ScanPath); err != nil {
		return nil, err
	}
	if c.Dirs.Templates, err = filepath.Abs(c.Dirs.Templates); err != nil {
		return nil, err
	}
	if c.Dirs.Static, err = filepath.Abs(c.Dirs.Static); err != nil {
		return nil, err
	}

	// Use one static asset for both the brand mark and favicon; retain legacy config.
	if c.Meta.Logo != "" {
		c.Meta.Favicon = c.Meta.Logo
	}
	if c.Meta.Favicon == "" {
		c.Meta.Favicon = "favicon.svg"
	}
	c.Meta.FaviconHref = "/static/" + c.Meta.Favicon
	switch strings.ToLower(filepath.Ext(c.Meta.Favicon)) {
	case ".svg":
		c.Meta.FaviconType = "image/svg+xml"
	case ".png":
		c.Meta.FaviconType = "image/png"
	case ".jpg", ".jpeg":
		c.Meta.FaviconType = "image/jpeg"
	case ".gif":
		c.Meta.FaviconType = "image/gif"
	case ".webp":
		c.Meta.FaviconType = "image/webp"
	case ".avif":
		c.Meta.FaviconType = "image/avif"
	default:
		c.Meta.FaviconType = "image/x-icon"
	}

	return c, nil
}
