package main

import (
	"testing"

	"github.com/gearboxlogic/tappet/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestApplyOverrides(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{McpProxy: &config.MCPProxyConfigV2{
		Addr:           ":9090",
		HierarchyPath:  "/configured/hierarchy",
		CapabilityPath: "/configured/capabilities",
	}}

	applyOverrides(cfg, "8080", "/flag/hierarchy", "/flag/capabilities")

	assert.Equal(t, ":8080", cfg.McpProxy.Addr)
	assert.Empty(t, cfg.McpProxy.HierarchyPath)
	assert.Equal(t, "/flag/capabilities", cfg.McpProxy.CapabilityPath)
}

func TestHierarchyOverrideSelectsCompatibilityBackend(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{McpProxy: &config.MCPProxyConfigV2{
		HierarchyPath:  "/configured/hierarchy",
		CapabilityPath: "/configured/capabilities",
	}}

	applyOverrides(cfg, "", "/flag/hierarchy", "")

	assert.Equal(t, "/flag/hierarchy", cfg.McpProxy.HierarchyPath)
	assert.Empty(t, cfg.McpProxy.CapabilityPath)
}

func TestApplyOverridesKeepsConfigWhenFlagsAreUnset(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{McpProxy: &config.MCPProxyConfigV2{
		Addr:           ":9090",
		HierarchyPath:  "/configured/hierarchy",
		CapabilityPath: "/configured/capabilities",
	}}

	applyOverrides(cfg, "", "", "")

	assert.Equal(t, ":9090", cfg.McpProxy.Addr)
	assert.Equal(t, "/configured/hierarchy", cfg.McpProxy.HierarchyPath)
	assert.Equal(t, "/configured/capabilities", cfg.McpProxy.CapabilityPath)
}
