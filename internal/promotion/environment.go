package promotion

import (
	"fmt"
	"strings"
)

// Requirement is an Environment whose Orders a Preparation must run after,
// as the Environment it deploys to requires.
type Requirement struct {
	// Environment is the name of the Environment.
	Environment string
	// MinSoakSeconds is the least soak the stage must have.
	MinSoakSeconds int32
}

// Required checks that stages, the upstream stages a Preparation names,
// include for every requirement an Order of its Environment with at least
// its soak. environments maps the Orders of stages to the Environment each
// deploys to; an Order that names none, or does not exist, is missing. It
// returns "" when every requirement holds, and otherwise what is missing.
func Required(stages []Stage, environments map[string]string, requirements []Requirement) string {
	var missing []string
	for _, requirement := range requirements {
		met := false
		for _, stage := range stages {
			if environments[stage.Order] == requirement.Environment && stage.SoakSeconds >= requirement.MinSoakSeconds {
				met = true
				break
			}
		}
		if !met {
			missing = append(missing, fmt.Sprintf("an Order of Environment %s with a soak of at least %ds",
				requirement.Environment, requirement.MinSoakSeconds))
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return "the Environment requires running after " + strings.Join(missing, " and after ")
}
