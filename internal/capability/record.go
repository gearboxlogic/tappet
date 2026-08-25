package capability

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"mime"
	"path/filepath"
	"sort"
)

type ArtifactKind string

const (
	ArtifactManifest      ArtifactKind = "manifest"
	ArtifactSkill         ArtifactKind = "skill"
	ArtifactSkillResource ArtifactKind = "skill_resource"
	ArtifactContext       ArtifactKind = "context"
)

type Artifact struct {
	Ref       string
	Kind      ArtifactKind
	ID        string
	Path      string
	MediaType string
	Bytes     int64
	SHA256    string
}

type Skill struct {
	Path      string
	Metadata  SkillMetadata
	Artifact  Artifact
	Resources []Artifact
}

// Record is one immutable normalized capability generation.
type Record struct {
	metadata       Metadata
	parent         string
	manifestDigest string
	skills         []Skill
	operations     []Operation
	context        []Artifact
	providers      []ProviderBinding
	artifacts      map[string]Artifact
	snapshot       *Snapshot
}

func (r *Record) Metadata() Metadata {
	metadata := r.metadata
	metadata.Tags = append([]string(nil), r.metadata.Tags...)
	return metadata
}

func (r *Record) Parent() string         { return r.parent }
func (r *Record) ManifestDigest() string { return r.manifestDigest }

func (r *Record) Skills() []Skill {
	result := make([]Skill, len(r.skills))
	for index, skill := range r.skills {
		result[index] = cloneSkill(skill)
	}
	return result
}

func (r *Record) Operations() []Operation {
	return append([]Operation(nil), r.operations...)
}

func (r *Record) Context() []Artifact {
	return append([]Artifact(nil), r.context...)
}

func (r *Record) Providers() []ProviderBinding {
	return append([]ProviderBinding(nil), r.providers...)
}

func (r *Record) Artifact(ref string) (Artifact, bool) {
	artifact, ok := r.artifacts[ref]
	return artifact, ok
}

func (r *Record) ReadArtifact(ref string) ([]byte, bool) {
	artifact, ok := r.artifacts[ref]
	if !ok || r.snapshot == nil {
		return nil, false
	}
	return r.snapshot.Read(artifact.Path)
}

func (r *Record) acquireArtifact(ref string) (Artifact, *artifactLease, bool) {
	artifact, ok := r.artifacts[ref]
	if !ok || r.snapshot == nil {
		return Artifact{}, nil, false
	}
	lease, ok := r.snapshot.acquireArtifact(artifact.Path, artifact.Bytes, artifact.SHA256)
	if !ok {
		return Artifact{}, nil, false
	}
	return artifact, lease, true
}

func (r *Record) release() {
	if r.snapshot != nil {
		r.snapshot.release()
	}
}

func cloneSkill(skill Skill) Skill {
	result := skill
	result.Metadata.Metadata = cloneStringMap(skill.Metadata.Metadata)
	result.Resources = append([]Artifact(nil), skill.Resources...)
	return result
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func newArtifact(manifest Manifest, manifestDigest string, kind ArtifactKind, id, packagePath string, staged stagedArtifact) Artifact {
	digest := hex.EncodeToString(staged.digest[:])
	refSource := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s", manifest.Metadata.ID, manifest.Metadata.Version, manifestDigest, kind, id, digest)
	refDigest := sha256.Sum256([]byte(refSource))
	mediaType := mime.TypeByExtension(filepath.Ext(packagePath))
	if mediaType == "" {
		mediaType = "text/plain; charset=utf-8"
	}
	return Artifact{
		Ref:       "artifact:v1:" + hex.EncodeToString(refDigest[:]),
		Kind:      kind,
		ID:        id,
		Path:      packagePath,
		MediaType: mediaType,
		Bytes:     int64(len(staged.data)),
		SHA256:    digest,
	}
}

func sortRecord(record *Record) {
	sort.Slice(record.skills, func(i, j int) bool { return record.skills[i].Path < record.skills[j].Path })
	for index := range record.skills {
		sort.Slice(record.skills[index].Resources, func(i, j int) bool {
			return record.skills[index].Resources[i].ID < record.skills[index].Resources[j].ID
		})
	}
	sort.Slice(record.operations, func(i, j int) bool { return record.operations[i].ID < record.operations[j].ID })
	sort.Slice(record.context, func(i, j int) bool { return record.context[i].ID < record.context[j].ID })
	sort.Slice(record.providers, func(i, j int) bool { return record.providers[i].ID < record.providers[j].ID })
}
