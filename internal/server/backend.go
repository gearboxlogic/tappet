package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gearboxlogic/tappet/internal/capability"
	"github.com/gearboxlogic/tappet/internal/hierarchy"
	"github.com/mark3labs/mcp-go/mcp"
)

type brokerBackend interface {
	RootOverview() string
	Browse(string) (map[string]interface{}, error)
	Execute(context.Context, string, map[string]interface{}) (*mcp.CallToolResult, error)
}

type hierarchyBackend struct {
	hierarchy *hierarchy.Hierarchy
	providers *hierarchy.ServerRegistry
}

func (b hierarchyBackend) RootOverview() string {
	root := b.hierarchy.GetRootNode()
	if root == nil {
		return ""
	}
	return root.Overview
}

func (b hierarchyBackend) Browse(path string) (map[string]interface{}, error) {
	return b.hierarchy.HandleGetToolsInCategory(path)
}

func (b hierarchyBackend) Execute(ctx context.Context, toolPath string, arguments map[string]interface{}) (*mcp.CallToolResult, error) {
	return b.hierarchy.HandleExecuteTool(ctx, b.providers, toolPath, arguments)
}

type capabilityBackend struct {
	capabilities *capability.Registry
	providers    *hierarchy.ServerRegistry
}

func (b capabilityBackend) RootOverview() string {
	return "Tappet capability registry. Browse categories to find validated capability operations."
}

func (b capabilityBackend) Browse(requestedPath string) (map[string]interface{}, error) {
	view, err := b.capabilities.Browse(requestedPath)
	if err != nil {
		return nil, err
	}
	response := map[string]interface{}{"path": view.Path}
	children := make(map[string]interface{}, len(view.Children))
	aggregatedTools := make(map[string]interface{})
	allChildrenAreLeaves := len(view.Children) > 0
	for _, child := range view.Children {
		childName := lastPathSegment(child.Path)
		if child.CapabilityID == "" {
			allChildrenAreLeaves = false
			children[childName] = map[string]interface{}{}
			continue
		}
		lease, lookupErr := b.capabilities.Lookup(child.CapabilityID)
		if lookupErr != nil {
			return nil, lookupErr
		}
		record := lease.Record()
		operations := record.Operations()
		if len(operations) == 1 {
			childName = operations[0].Target
		}
		if _, duplicate := children[childName]; duplicate {
			lease.Release()
			return nil, fmt.Errorf("duplicate hierarchy child name %q under %s", childName, view.Path)
		}
		children[childName] = map[string]interface{}{
			"is_leaf":    true,
			"tool_count": len(operations),
			"capability": child.CapabilityID,
		}
		for _, operation := range operations {
			toolName := operation.Target
			if _, duplicate := aggregatedTools[toolName]; duplicate {
				lease.Release()
				return nil, fmt.Errorf("duplicate operation target %q under %s", toolName, view.Path)
			}
			aggregatedTools[toolName] = map[string]interface{}{
				"description":   operation.Description,
				"tool_path":     legacyToolPath(record, operation),
				"capability_id": child.CapabilityID,
				"operation_id":  operation.ID,
			}
		}
		lease.Release()
	}
	if len(children) > 0 {
		response["children"] = children
	}

	if view.CapabilityID != "" {
		lease, lookupErr := b.capabilities.Lookup(view.CapabilityID)
		if lookupErr != nil {
			return nil, lookupErr
		}
		tools := make(map[string]interface{})
		for _, operation := range lease.Record().Operations() {
			tools[operation.Target] = map[string]interface{}{
				"description":   operation.Description,
				"tool_path":     legacyToolPath(lease.Record(), operation),
				"capability_id": view.CapabilityID,
				"operation_id":  operation.ID,
			}
		}
		lease.Release()
		response["tools"] = tools
	} else if allChildrenAreLeaves {
		response["tools"] = aggregatedTools
	} else {
		response["tools"] = map[string]interface{}{}
	}
	return response, nil
}

func (b capabilityBackend) Execute(ctx context.Context, toolPath string, arguments map[string]interface{}) (*mcp.CallToolResult, error) {
	lease, err := b.capabilities.ResolveToolPath(toolPath)
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	binding := lease.ProviderBinding()
	operation := lease.Operation()
	if binding.Type != "mcp" {
		return nil, errors.New("capability provider type is not executable by the MCP adapter")
	}
	return hierarchy.ExecuteMappedTool(ctx, b.providers, lease.CapabilityID()+"/"+operation.ID, binding.ServerRef, operation.Target, arguments, nil)
}

func legacyToolPath(record *capability.Record, operation capability.Operation) string {
	operations := record.Operations()
	if len(operations) == 1 {
		if record.Parent() == "" {
			return operation.Target
		}
		return record.Parent() + "." + operation.Target
	}
	return record.Metadata().ID + "." + operation.ID
}

func lastPathSegment(path string) string {
	if separator := strings.LastIndexByte(path, '.'); separator >= 0 {
		return path[separator+1:]
	}
	return path
}
