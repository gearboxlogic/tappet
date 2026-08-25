package capability

const (
	ManifestMaxBytes        = 1 << 20
	ManifestMaxDepth        = 64
	ManifestMaxNodes        = 16_384
	SkillMaxBytes           = 1 << 20
	SkillFrontmatterMax     = 64 << 10
	ArtifactMaxBytes        = 4 << 20
	PackageArtifactMax      = 64 << 20
	MaxSkills               = 32
	MaxSkillResources       = 128
	MaxResourcesPerSkill    = 32
	MaxOperations           = 128
	MaxContextReferences    = 128
	MaxProviders            = 32
	MaxStructureItemBytes   = 4_096
	MaxStructureBytes       = 512 << 10
	MaxCapabilityIDBytes    = 128
	MaxNameBytes            = 128
	MaxVersionBytes         = 64
	MaxDescriptionBytes     = 1_024
	MaxHierarchyPathBytes   = 256
	MaxTags                 = 16
	MaxTagBytes             = 64
	MaxAllTagsBytes         = 512
	MaxBaseCardBytes        = 4_096
	MaxSkillPathBytes       = 512
	MaxSkillNameBytes       = 128
	MaxSkillDescription     = 1_024
	MaxResourceIDBytes      = 128
	MaxResourceKindBytes    = 32
	MaxResourcePathBytes    = 512
	MaxOperationIDBytes     = 128
	MaxOperationDescription = 1_024
	MaxProviderAliasBytes   = 128
	MaxOperationTargetBytes = 256
	MaxContextIDBytes       = 128
	MaxContextPathBytes     = 512
	MaxProviderIDBytes      = 128
	MaxProviderTypeBytes    = 32
	MaxServerRefBytes       = 256
)
