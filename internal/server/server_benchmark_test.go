package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/gearboxlogic/tappet/internal/hierarchy"
	"github.com/mark3labs/mcp-go/mcp"
)

func BenchmarkNewTappetServer(b *testing.B) {
	h := loadBenchmarkHierarchy(b)
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		registry := hierarchy.NewServerRegistry(nil)
		server, err := NewTappetServer(ServerDependencies{
			Hierarchy: h,
			Registry:  registry,
			Name:      "Tappet",
			Version:   "benchmark",
		})
		if err != nil {
			b.Fatal(err)
		}
		if len(server.ListTools()) != 2 {
			b.Fatalf("unexpected outward tool count: %d", len(server.ListTools()))
		}
		registry.Close()
	}
}

func BenchmarkOutwardToolSurfaceEncoding(b *testing.B) {
	h := loadBenchmarkHierarchy(b)
	registry := hierarchy.NewServerRegistry(nil)
	b.Cleanup(registry.Close)
	server, err := NewTappetServer(ServerDependencies{
		Hierarchy: h,
		Registry:  registry,
		Name:      "Tappet",
		Version:   "benchmark",
	})
	if err != nil {
		b.Fatal(err)
	}

	registered := server.ListTools()
	tools := make([]mcp.Tool, 0, len(registered))
	for _, entry := range registered {
		tools = append(tools, entry.Tool)
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	payload, err := json.Marshal(tools)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		if _, err := json.Marshal(tools); err != nil {
			b.Fatal(err)
		}
	}

	b.ReportMetric(float64(len(tools)), "tools")
	b.ReportMetric(float64(len(payload)), "schema_bytes")
}

func loadBenchmarkHierarchy(b *testing.B) *hierarchy.Hierarchy {
	b.Helper()
	dir := b.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "root.json"), []byte(`{"overview":"benchmark root"}`), 0o644); err != nil {
		b.Fatal(err)
	}
	h, err := hierarchy.LoadHierarchy(dir)
	if err != nil {
		b.Fatal(err)
	}
	return h
}
