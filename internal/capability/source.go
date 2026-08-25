package capability

import (
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
)

var errLocalPackageIngestionUnsupported = errors.New("safe local package ingestion is unsupported on this platform")

type packageRoot interface {
	PackageNames() ([]string, error)
	OpenPackage(string) (packageDirectory, error)
	Close() error
}

type packageDirectory interface {
	OpenRegularFile(string) (*os.File, int64, error)
	Close() error
}

func validatePackageComponent(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\\`) {
		return fmt.Errorf("invalid package directory name %q", name)
	}
	return nil
}

func splitSafePath(relative string) ([]string, error) {
	if relative == "" || relative != path.Clean(relative) || strings.HasPrefix(relative, "/") || strings.Contains(relative, "\\") {
		return nil, fmt.Errorf("unsafe package-relative path %q", relative)
	}
	parts := strings.Split(relative, "/")
	for _, part := range parts {
		if err := validatePackageComponent(part); err != nil {
			return nil, err
		}
	}
	return parts, nil
}

func sortedPackageNames(names []string) ([]string, error) {
	result := append([]string(nil), names...)
	for _, name := range result {
		if err := validatePackageComponent(name); err != nil {
			return nil, err
		}
	}
	sort.Strings(result)
	return result, nil
}
