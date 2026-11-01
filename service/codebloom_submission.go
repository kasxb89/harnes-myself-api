package service

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/dto"
)

// ProtectCodeBloomImageSubmissionFailure is applied only after the caller starts
// this non-idempotent provider POST. An upstream result cannot be inferred from
// a timeout or malformed body, and its raw diagnostics must not reach clients.
func ProtectCodeBloomImageSubmissionFailure(taskErr *dto.TaskError) *dto.TaskError {
	if taskErr == nil {
		return nil
	}
	return &dto.TaskError{NoRetry: true, Code: "image_submission_unconfirmed", Message: "The image upstream did not return a verified result; do not submit again automatically", Data: nil, StatusCode: http.StatusBadGateway, LocalError: true, Error: errors.New("image submission outcome is unconfirmed")}
}
