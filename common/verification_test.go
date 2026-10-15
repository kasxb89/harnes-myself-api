package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The SMS template carries a numeric placeholder and the provider rejects a
// code with letters, so a generator drawn from hex digits does not work here.
// Repeated draws are what make that observable: one hex-based code passes by
// chance about six times in a hundred.
func TestGenerateNumericVerificationCode_OnlyEverProducesDigits(t *testing.T) {
	for range 50 {
		code, err := GenerateNumericVerificationCode(6)
		require.NoError(t, err)
		assert.Len(t, code, 6)
		for _, digit := range code {
			assert.GreaterOrEqual(t, digit, '0')
			assert.LessOrEqual(t, digit, '9')
		}
	}
}

func TestGenerateNumericVerificationCode_RefusesAnEmptyLength(t *testing.T) {
	for _, length := range []int{0, -1} {
		_, err := GenerateNumericVerificationCode(length)
		assert.Error(t, err)
	}
}
