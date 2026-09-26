package cloudformation

import cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"

// maskedValue is what CloudFormation shows in place of a NoEcho value.
const maskedValue = "****"

// maskParameters returns a copy of params with every NoEcho value masked, the
// way CloudFormation shows them in stack reads. The stored values stay
// unmasked so !Ref resolves the real value.
func maskParameters(params []cfn.Parameter) []cfn.Parameter {
	out := make([]cfn.Parameter, len(params))

	for i, p := range params {
		if p.NoEcho {
			p.Value = maskedValue
		}

		out[i] = p
	}

	return out
}
