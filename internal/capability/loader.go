package capability

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"unicode/utf8"
)

const manifestFileName = "tappet.yaml"

// Loader copies package artifacts into a private bounded store before it parses
// or validates them.
type Loader struct {
	rootPath   string
	store      *SnapshotStore
	afterStage func()
}

func NewLoader(rootPath string, store *SnapshotStore) (*Loader, error) {
	if rootPath == "" {
		return nil, errors.New("capability package root is required")
	}
	if store == nil {
		return nil, errors.New("snapshot store is required")
	}
	return &Loader{rootPath: rootPath, store: store}, nil
}

func (l *Loader) LoadAll() ([]*Record, error) {
	root, err := openPackageRoot(l.rootPath)
	if err != nil {
		return nil, packageError("package_root_invalid", l.rootPath, err)
	}
	defer root.Close()
	names, err := root.PackageNames()
	if err != nil {
		return nil, packageError("package_root_invalid", l.rootPath, err)
	}
	records := make([]*Record, 0, len(names))
	seenIDs := make(map[string]struct{}, len(names))
	for _, name := range names {
		record, loadErr := l.loadFromRoot(root, name)
		if loadErr != nil {
			for _, loaded := range records {
				loaded.release()
			}
			return nil, loadErr
		}
		id := record.metadata.ID
		if _, exists := seenIDs[id]; exists {
			record.release()
			for _, loaded := range records {
				loaded.release()
			}
			return nil, packageError("package_duplicate_capability", name, fmt.Errorf("duplicate capability ID %q", id))
		}
		seenIDs[id] = struct{}{}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].metadata.ID < records[j].metadata.ID })
	return records, nil
}

func (l *Loader) Load(name string) (*Record, error) {
	root, err := openPackageRoot(l.rootPath)
	if err != nil {
		return nil, packageError("package_root_invalid", l.rootPath, err)
	}
	defer root.Close()
	return l.loadFromRoot(root, name)
}

type artifactSource struct {
	spec artifactSpec
	file *os.File
	size int64
}

type artifactSpec struct {
	path    string
	kind    ArtifactKind
	id      string
	maximum int64
}

func (l *Loader) loadFromRoot(root packageRoot, packageName string) (_ *Record, returnedErr error) {
	if err := validatePackageComponent(packageName); err != nil {
		return nil, packageError("package_directory_invalid", packageName, err)
	}
	directory, err := root.OpenPackage(packageName)
	if err != nil {
		return nil, packageError("package_directory_invalid", packageName, err)
	}
	defer directory.Close()

	transaction := l.store.begin()
	defer func() {
		if returnedErr != nil {
			transaction.abort()
		}
	}()

	manifestFile, manifestSize, err := directory.OpenRegularFile(manifestFileName)
	if err != nil {
		return nil, packageError("package_manifest_artifact_invalid", packageName+"/"+manifestFileName, err)
	}
	if manifestSize > ManifestMaxBytes {
		manifestFile.Close()
		return nil, packageError("package_manifest_limit_exceeded", packageName+"/"+manifestFileName, fmt.Errorf("declared size %d exceeds %d", manifestSize, ManifestMaxBytes))
	}
	if err := transaction.reserve(manifestSize); err != nil {
		manifestFile.Close()
		return nil, packageError("package_staging_capacity_exhausted", packageName, err)
	}
	if err := transaction.stageReserved(manifestFileName, manifestFile, manifestSize, ManifestMaxBytes); err != nil {
		manifestFile.Close()
		return nil, packageError("package_manifest_staging_failed", packageName+"/"+manifestFileName, err)
	}
	if err := manifestFile.Close(); err != nil {
		return nil, packageError("package_manifest_staging_failed", packageName+"/"+manifestFileName, err)
	}
	manifestBytes, _ := transaction.bytes(manifestFileName)
	manifest, err := ParseManifest(manifestBytes)
	clear(manifestBytes)
	if err != nil {
		return nil, err
	}
	if manifest.Metadata.ID != packageName {
		return nil, packageError("package_directory_mismatch", packageName, fmt.Errorf("directory name must match capability ID %q", manifest.Metadata.ID))
	}

	specs, err := artifactSpecs(manifest)
	if err != nil {
		return nil, err
	}
	sources := make([]artifactSource, 0, len(specs))
	defer func() {
		for _, source := range sources {
			if source.file != nil {
				_ = source.file.Close()
			}
		}
	}()
	remainingSize := int64(0)
	for _, spec := range specs {
		file, size, openErr := directory.OpenRegularFile(spec.path)
		if openErr != nil {
			return nil, packageError("package_artifact_invalid", packageName+"/"+spec.path, openErr)
		}
		if size > spec.maximum {
			file.Close()
			return nil, packageError("package_artifact_limit_exceeded", packageName+"/"+spec.path, fmt.Errorf("declared size %d exceeds %d", size, spec.maximum))
		}
		remainingSize += size
		sources = append(sources, artifactSource{spec: spec, file: file, size: size})
	}
	if err := transaction.reserve(remainingSize); err != nil {
		return nil, packageError("package_staging_capacity_exhausted", packageName, err)
	}
	for index := range sources {
		source := &sources[index]
		if err := transaction.stageReserved(source.spec.path, source.file, source.size, source.spec.maximum); err != nil {
			return nil, packageError("package_artifact_staging_failed", packageName+"/"+source.spec.path, err)
		}
		if err := source.file.Close(); err != nil {
			return nil, packageError("package_artifact_staging_failed", packageName+"/"+source.spec.path, err)
		}
		source.file = nil
	}
	if l.afterStage != nil {
		l.afterStage()
	}

	record, err := normalizeStagedPackage(manifest, transaction)
	if err != nil {
		return nil, err
	}
	snapshot, err := transaction.commit()
	if err != nil {
		return nil, packageError("package_snapshot_commit_failed", packageName, err)
	}
	record.snapshot = snapshot
	return record, nil
}

func artifactSpecs(manifest Manifest) ([]artifactSpec, error) {
	specs := make([]artifactSpec, 0, len(manifest.Spec.Skills)+MaxSkillResources+len(manifest.Spec.Context))
	seen := map[string]struct{}{manifestFileName: {}}
	add := func(spec artifactSpec) error {
		if _, exists := seen[spec.path]; exists {
			return packageError("package_duplicate_artifact", spec.path, errors.New("artifact path is used more than once"))
		}
		seen[spec.path] = struct{}{}
		specs = append(specs, spec)
		return nil
	}
	for _, skill := range manifest.Spec.Skills {
		skillFile := path.Join(skill.Path, "SKILL.md")
		if err := add(artifactSpec{path: skillFile, kind: ArtifactSkill, id: skill.Path, maximum: SkillMaxBytes}); err != nil {
			return nil, err
		}
		for _, resource := range skill.Resources {
			resourcePath := path.Join(skill.Path, resource.Path)
			if err := add(artifactSpec{path: resourcePath, kind: ArtifactSkillResource, id: skill.Path + "/" + resource.ID, maximum: ArtifactMaxBytes}); err != nil {
				return nil, err
			}
		}
	}
	for _, contextRef := range manifest.Spec.Context {
		if err := add(artifactSpec{path: contextRef.Path, kind: ArtifactContext, id: contextRef.ID, maximum: ArtifactMaxBytes}); err != nil {
			return nil, err
		}
	}
	return specs, nil
}

func normalizeStagedPackage(manifest Manifest, transaction *stagingTransaction) (*Record, error) {
	manifestArtifact := transaction.artifacts[manifestFileName]
	manifestDigest := hex.EncodeToString(manifestArtifact.digest[:])
	record := &Record{
		metadata:       manifest.Metadata,
		parent:         manifest.Spec.Parent,
		manifestDigest: manifestDigest,
		operations:     append([]Operation(nil), manifest.Spec.Operations...),
		providers:      append([]ProviderBinding(nil), manifest.Spec.Providers...),
		artifacts:      make(map[string]Artifact),
	}
	manifestDescriptor := newArtifact(manifest, manifestDigest, ArtifactManifest, "manifest", manifestFileName, manifestArtifact)
	record.artifacts[manifestDescriptor.Ref] = manifestDescriptor

	for _, skill := range manifest.Spec.Skills {
		skillPath := path.Join(skill.Path, "SKILL.md")
		staged := transaction.artifacts[skillPath]
		document, err := ParseSkill(staged.data, skill.Path)
		if err != nil {
			return nil, err
		}
		descriptor := newArtifact(manifest, manifestDigest, ArtifactSkill, document.Metadata.Name, skillPath, staged)
		skillRecord := Skill{Path: skill.Path, Metadata: document.Metadata, Artifact: descriptor}
		record.artifacts[descriptor.Ref] = descriptor
		for _, resource := range skill.Resources {
			resourcePath := path.Join(skill.Path, resource.Path)
			resourceStaged := transaction.artifacts[resourcePath]
			if !utf8.Valid(resourceStaged.data) {
				return nil, packageError("package_artifact_invalid", resourcePath, errors.New("listed skill reference must be valid UTF-8 text"))
			}
			resourceDescriptor := newArtifact(manifest, manifestDigest, ArtifactSkillResource, resource.ID, resourcePath, resourceStaged)
			skillRecord.Resources = append(skillRecord.Resources, resourceDescriptor)
			record.artifacts[resourceDescriptor.Ref] = resourceDescriptor
		}
		record.skills = append(record.skills, skillRecord)
	}
	for _, contextRef := range manifest.Spec.Context {
		staged := transaction.artifacts[contextRef.Path]
		if !utf8.Valid(staged.data) {
			return nil, packageError("package_artifact_invalid", contextRef.Path, errors.New("context reference must be valid UTF-8 text"))
		}
		descriptor := newArtifact(manifest, manifestDigest, ArtifactContext, contextRef.ID, contextRef.Path, staged)
		record.context = append(record.context, descriptor)
		record.artifacts[descriptor.Ref] = descriptor
	}
	sortRecord(record)
	return record, nil
}

func digestBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
