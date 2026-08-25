//go:build linux || darwin

package capability

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type unixPackageRoot struct {
	file *os.File
}

type unixPackageDirectory struct {
	file *os.File
}

func openPackageRoot(rootPath string) (packageRoot, error) {
	absolute, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, fmt.Errorf("resolve package root: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve canonical package root: %w", err)
	}
	fd, err := unix.Open(canonical, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open package root: %w", err)
	}
	return &unixPackageRoot{file: os.NewFile(uintptr(fd), canonical)}, nil
}

func (r *unixPackageRoot) PackageNames() ([]string, error) {
	fd, err := unix.Dup(int(r.file.Fd()))
	if err != nil {
		return nil, fmt.Errorf("duplicate package root handle: %w", err)
	}
	copyFile := os.NewFile(uintptr(fd), r.file.Name())
	defer copyFile.Close()
	entries, err := copyFile.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("enumerate package root: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() == "." || entry.Name() == ".." {
			continue
		}
		names = append(names, entry.Name())
	}
	return sortedPackageNames(names)
}

func (r *unixPackageRoot) OpenPackage(name string) (packageDirectory, error) {
	if err := validatePackageComponent(name); err != nil {
		return nil, err
	}
	fd, err := unix.Openat(int(r.file.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open package directory %q: %w", name, err)
	}
	return &unixPackageDirectory{file: os.NewFile(uintptr(fd), name)}, nil
}

func (r *unixPackageRoot) Close() error { return r.file.Close() }

func (d *unixPackageDirectory) OpenRegularFile(relative string) (*os.File, int64, error) {
	parts, err := splitSafePath(relative)
	if err != nil {
		return nil, 0, err
	}
	directoryFD, err := unix.Dup(int(d.file.Fd()))
	if err != nil {
		return nil, 0, fmt.Errorf("duplicate package directory handle: %w", err)
	}
	defer func() {
		if directoryFD >= 0 {
			_ = unix.Close(directoryFD)
		}
	}()

	for _, component := range parts[:len(parts)-1] {
		nextFD, openErr := unix.Openat(directoryFD, component, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if openErr != nil {
			return nil, 0, fmt.Errorf("open package path component %q: %w", component, openErr)
		}
		_ = unix.Close(directoryFD)
		directoryFD = nextFD
	}

	name := parts[len(parts)-1]
	fd, err := unix.Openat(directoryFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, 0, fmt.Errorf("open package artifact %q: %w", relative, err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return nil, 0, fmt.Errorf("inspect package artifact %q: %w", relative, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(fd)
		return nil, 0, fmt.Errorf("package artifact %q is not a regular file", relative)
	}
	return os.NewFile(uintptr(fd), relative), stat.Size, nil
}

func (d *unixPackageDirectory) Close() error { return d.file.Close() }
