package common

import "math"

// MaxRelayOutputTokens is the existing request budget safety bound shared by
// validation and advertised product capacities.
const MaxRelayOutputTokens = math.MaxInt32 / 2

// MaxTaskDurationSeconds is also used by administrative generation schemas.
// All request and configuration paths share the same billing quantity bound.
const MaxTaskDurationSeconds = 3600
