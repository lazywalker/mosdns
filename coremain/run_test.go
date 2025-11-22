package coremain

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadConfig_EnvOverride verifies that environment variables set via
// viper's AutomaticEnv (with SetEnvKeyReplacer) correctly override values
// from the configuration file.
func TestLoadConfig_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	// Write a minimal config with a hosts plugin where auto_reload is false
	cfgContent := `plugins:
  - tag: hosts
    type: hosts
    args:
      auto_reload: false
`
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	// Set environment variable to override plugins.hosts.args.auto_reload
	// Note: loadConfig uses SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	// and AutomaticEnv(), so dots are converted to underscores.
	if err := os.Setenv("PLUGINS_HOSTS_ARGS_AUTO_RELOAD", "true"); err != nil {
		t.Fatalf("failed to set env: %v", err)
	}
	defer os.Unsetenv("PLUGINS_HOSTS_ARGS_AUTO_RELOAD")

	cfg, _, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatalf("loadConfig returned error: %v", err)
	}

	// Find the hosts plugin in cfg.Plugins and check args.auto_reload
	found := false
	for _, p := range cfg.Plugins {
		if p.Tag == "hosts" || p.Type == "hosts" {
			found = true
			// Args should be decoded into a map[string]any by viper/mapstructure.
			if argsMap, ok := p.Args.(map[string]any); ok {
				t.Logf("plugin args for hosts: %#v", argsMap)
				if v, ok := argsMap["auto_reload"]; ok {
					// Expect the environment override to produce a boolean true
					if bv, ok := v.(bool); ok {
						if !bv {
							t.Fatalf("expected auto_reload=true from env override, got false")
						}
					} else {
						t.Fatalf("auto_reload has unexpected type %T (value=%v)", v, v)
					}
				} else {
					t.Fatalf("auto_reload not present in plugin args: %#v", argsMap)
				}
			} else {
				t.Fatalf("plugin args not a map[string]any: %#v", p.Args)
			}
		}
	}
	if !found {
		t.Fatalf("hosts plugin not found in config: %#v", cfg.Plugins)
	}
}

// TestLoadConfig_PluginPasswdEnvOverride verifies that plugin-level passwd
// can be overridden via PLUGINS_<TAG>_ARGS_PASSWD environment variable.
func TestLoadConfig_PluginPasswdEnvOverride(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	// Write a minimal config with ros_addrlist plugin and passwd set
	cfgContent := "plugins:\n" +
		"  - tag: add_gfwlist\n" +
		"    type: ros_addrlist\n" +
		"    args:\n" +
		"      addrlist: \"mosdns-gfwlist\"\n" +
		"      server: \"http://192.168.88.1:80\"\n" +
		"      user: \"mosdns\"\n" +
		"      passwd: \"Mosisgood\"\n" +
		"      mask4: 24\n" +
		"      mask6: 32\n"
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	// Override via env
	if err := os.Setenv("PLUGINS_ADD_GFWLIST_ARGS_PASSWD", "SuperSecret"); err != nil {
		t.Fatalf("failed to set env: %v", err)
	}
	defer os.Unsetenv("PLUGINS_ADD_GFWLIST_ARGS_PASSWD")

	cfg, _, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatalf("loadConfig returned error: %v", err)
	}

	found := false
	for _, p := range cfg.Plugins {
		if p.Tag == "add_gfwlist" || p.Type == "ros_addrlist" {
			found = true
			argsMap, ok := p.Args.(map[string]any)
			if !ok {
				t.Fatalf("plugin args not a map[string]any: %#v", p.Args)
			}
			if pw, ok := argsMap["passwd"]; ok {
				if ps, ok := pw.(string); ok {
					if ps != "SuperSecret" {
						t.Fatalf("expected passwd SuperSecret from env override, got %q", ps)
					}
				} else {
					t.Fatalf("passwd has unexpected type %T (value=%v)", pw, pw)
				}
			} else {
				t.Fatalf("passwd not present in plugin args: %#v", argsMap)
			}
		}
	}
	if !found {
		t.Fatalf("add_gfwlist plugin not found in config: %#v", cfg.Plugins)
	}
}
