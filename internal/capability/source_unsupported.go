//go:build !linux && !darwin

package capability

func openPackageRoot(string) (packageRoot, error) {
	return nil, errLocalPackageIngestionUnsupported
}
