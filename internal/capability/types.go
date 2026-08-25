package capability

// Manifest is the closed V1-alpha package manifest before artifact staging.
type Manifest struct {
	APIVersion string
	Kind       string
	Metadata   Metadata
	Spec       Spec
}

type Metadata struct {
	ID          string
	Name        string
	Version     string
	Description string
	Tags        []string
}

type Spec struct {
	Parent     string
	Skills     []SkillDeclaration
	Operations []Operation
	Context    []ContextReference
	Providers  []ProviderBinding
}

type SkillDeclaration struct {
	Path      string
	Resources []SkillResource
}

type SkillResource struct {
	ID   string
	Kind string
	Path string
}

type Operation struct {
	ID          string
	Description string
	Provider    string
	Target      string
}

type ContextReference struct {
	ID   string
	Path string
}

type ProviderBinding struct {
	ID        string
	Type      string
	ServerRef string
}

// SkillMetadata is the normalized discovery metadata from SKILL.md.
type SkillMetadata struct {
	Name          string
	Description   string
	License       string
	AllowedTools  string
	Compatibility string
	Metadata      map[string]string
}

// SkillDocument is a validated Agent Skills document. Raw package bytes remain
// in the immutable artifact snapshot rather than this metadata value.
type SkillDocument struct {
	Metadata SkillMetadata
	Body     string
}
