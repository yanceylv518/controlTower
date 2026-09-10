package archivecontrol

import "testing"

func TestConfigBounds(t *testing.T) {
	c := Default()
	c.AgentID = "agent"
	c.InstanceID = "instance"
	if !c.Validate() {
		t.Fatal("default config")
	}
	for _, mutate := range []func(*Config){func(c *Config) { c.BatchSize = 0 }, func(c *Config) { c.DelaySeconds = 59 }, func(c *Config) { c.IntervalSeconds = 0 }, func(c *Config) { c.Version = -1 }} {
		bad := c
		mutate(&bad)
		if bad.Validate() {
			t.Fatalf("accepted %+v", bad)
		}
	}
}
