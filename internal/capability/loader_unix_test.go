//go:build linux || darwin

package capability

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoaderRejectsSpecialAndLinkedArtifactsWithoutBlocking(t *testing.T) {
	testCases := []struct {
		name   string
		create func(*testing.T, string)
	}{
		{
			name: "fifo",
			create: func(t *testing.T, target string) {
				require.NoError(t, unix.Mkfifo(target, 0o600))
			},
		},
		{
			name: "directory",
			create: func(t *testing.T, target string) {
				require.NoError(t, os.Mkdir(target, 0o700))
			},
		},
		{
			name: "symlink",
			create: func(t *testing.T, target string) {
				require.NoError(t, os.Symlink("../tappet.yaml", target))
			},
		},
		{
			name: "socket",
			create: func(t *testing.T, target string) {
				shortPath := filepath.Join(os.TempDir(), fmt.Sprintf("tappet-capability-%d.sock", time.Now().UnixNano()))
				listener, err := net.Listen("unix", shortPath)
				if errors.Is(err, unix.EPERM) {
					t.Skip("sandbox does not permit Unix-domain sockets")
				}
				require.NoError(t, err)
				require.NoError(t, os.Rename(shortPath, target))
				t.Cleanup(func() {
					_ = listener.Close()
					_ = os.Remove(shortPath)
				})
			},
		},
		{
			name: "device",
			create: func(t *testing.T, target string) {
				err := unix.Mknod(target, unix.S_IFCHR|0o600, int(unix.Mkdev(1, 3)))
				if errors.Is(err, unix.EPERM) {
					t.Skip("test environment does not permit device nodes")
				}
				require.NoError(t, err)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			packageDir := writePackageFixture(t, root, "software.github.ci-debugging")
			target := filepath.Join(packageDir, "context", "repository-conventions.md")
			require.NoError(t, os.Remove(target))
			testCase.create(t, target)
			store := newTestSnapshotStore(t)
			loader, err := NewLoader(root, store)
			require.NoError(t, err)

			result := make(chan error, 1)
			go func() {
				_, loadErr := loader.Load("software.github.ci-debugging")
				result <- loadErr
			}()
			select {
			case err := <-result:
				require.Error(t, err)
				assert.Contains(t, err.Error(), "package_artifact_invalid")
			case <-time.After(time.Second):
				t.Fatal("special-file rejection blocked")
			}
			assert.Equal(t, StoreStats{}, store.Stats())
		})
	}
}

func TestLoaderRejectsSymlinkedIntermediateDirectory(t *testing.T) {
	root := t.TempDir()
	packageDir := writePackageFixture(t, root, "software.github.ci-debugging")
	realSkills := filepath.Join(packageDir, "real-skills")
	require.NoError(t, os.Rename(filepath.Join(packageDir, "skills"), realSkills))
	require.NoError(t, os.Symlink("real-skills", filepath.Join(packageDir, "skills")))
	store := newTestSnapshotStore(t)
	loader, err := NewLoader(root, store)
	require.NoError(t, err)

	_, err = loader.Load("software.github.ci-debugging")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "package_artifact_invalid")
}
