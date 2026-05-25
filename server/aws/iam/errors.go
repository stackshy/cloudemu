package iam

// AWS IAM uses a single NoSuchEntity code across every resource type
// (Users, Roles, Policies, Groups, AccessKeys, InstanceProfiles); the SDK
// maps it to *types.NoSuchEntityException for whichever operation it came
// from. Likewise EntityAlreadyExists is the universal duplicate-create code.
const (
	codeNoSuchEntity       = "NoSuchEntity"
	codeEntityAlreadyExist = "EntityAlreadyExists"
)

func notFoundCode(_ error) string      { return codeNoSuchEntity }
func alreadyExistsCode(_ error) string { return codeEntityAlreadyExist }
