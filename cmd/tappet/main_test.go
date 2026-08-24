package main

import (
	"testing"

	"github.com/gearboxlogic/tappet/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestApplyOverrides(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{McpProxy: &config.MCPProxyConfigV2{
		Addr:          ":9090",
		HierarchyPath: "/configured/hierarchy",
	}}

	applyOverrides(cfg, "8080", "/flag/hierarchy")

	assert.Equal(t, ":8080", cfg.McpProxy.Addr)
	assert.Equal(t, "/flag/hierarchy", cfg.McpProxy.HierarchyPath)
}

func TestApplyOverridesKeepsConfigWhenFlagsAreUnset(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{McpProxy: &config.MCPProxyConfigV2{
		Addr:          ":9090",
		HierarchyPath: "/configured/hierarchy",
	}}

	applyOverrides(cfg, "", "")

	assert.Equal(t, ":9090", cfg.McpProxy.Addr)
	assert.Equal(t, "/configured/hierarchy", cfg.McpProxy.HierarchyPath)
}
