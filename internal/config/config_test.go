package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCatalogRefreshInterval(t *testing.T) {
	for _, tt := range []struct {
		name, value string
		want        time.Duration
		invalid     bool
	}{
		{"default", "", 30 * time.Second, false},
		{"configured", "2m", 2 * time.Minute, false},
		{"zero", "0s", 0, true},
		{"negative", "-1s", 0, true},
		{"invalid", "soon", 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			yaml := "repo:\n  scanPath: ./repos\n"
			if tt.value != "" {
				yaml += "  refreshInterval: " + tt.value + "\n"
			}
			if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
				t.Fatal(err)
			}
			c, err := Read(path)
			if tt.invalid {
				if err == nil {
					t.Fatal("invalid refresh interval was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.Repo.RefreshInterval != tt.want {
				t.Fatalf("interval %s; want %s", c.Repo.RefreshInterval, tt.want)
			}
		})
	}
}
