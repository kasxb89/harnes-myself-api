package common

import "math"

// MaxRelayOutputTokens is the existing request budget safety bound shared by
// validation and advertised product capacities.
const MaxRelayOutputTokens = math.MaxInt32 / 2
