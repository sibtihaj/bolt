package retry

import "strings"

// ErrorClass indicates whether an error is safe to retry.
type ErrorClass int

const (
	ClassRetryable ErrorClass = iota // Transient — safe to retry with backoff
	ClassThrottle                    // Rate-limited — retry with longer backoff
	ClassFatal                       // Non-recoverable — stop immediately
)

// Classification is the result of classifying a command failure.
type Classification struct {
	Class  ErrorClass
	Reason string
	Detail string // the pattern that matched
}

// fatalPatterns — errors that will not be fixed by retrying.
var fatalPatterns = []string{
	"AccessDenied",
	"access denied",
	"Unauthorized",
	"unauthorized",
	"Forbidden",
	"forbidden",
	" 403 ",
	"(403)",
	"InvalidParameterException",
	"InvalidParameter",
	"ValidationException",
	"ResourceNotFoundException",
	"InvalidClientTokenId",
	"NoCredentialProviders",
	"CrashLoopBackOff",
	"ImagePullBackOff",
	"ErrImagePull",
	"invalid character",
	"no such file",
	"permission denied",
	"certificate signed by unknown authority",
	"x509:",
	"cannot unmarshal",
	"unknown flag",
	"unknown command",
	"already exists",
	"chart not found",
	"does not exist",
}

// retryablePatterns — transient errors worth retrying.
var retryablePatterns = []string{
	"ThrottlingException",
	"RequestThrottled",
	"RequestExpired",
	"SlowDown",
	"TooManyRequests",
	"ServiceUnavailable",
	"InternalFailure",
	"InternalError",
	"Internal Server Error",
	"connection refused",
	"connection reset by peer",
	"connection timed out",
	"i/o timeout",
	"context deadline exceeded",
	"dial tcp",
	"transport:",
	"EOF",
	"UPGRADE FAILED",
	"timed out waiting",
	"try again",
	"timeout",
	"Timeout",
	" 429",
	" 500",
	" 502",
	" 503",
	" 504",
	"(500)",
	"(503)",
	"(504)",
}

// throttlePatterns — a subset of retryable patterns that need a longer backoff.
var throttlePatterns = []string{
	"ThrottlingException",
	"RequestThrottled",
	"SlowDown",
	"TooManyRequests",
	" 429",
}

// Classify examines captured stderr output and the error message to determine
// whether the operation should be retried. Fatal matches take precedence.
func Classify(stderr string, err error) Classification {
	combined := stderr
	if err != nil {
		combined += " " + err.Error()
	}
	lower := strings.ToLower(combined)

	// Fatal patterns take precedence over retryable.
	for _, pat := range fatalPatterns {
		if strings.Contains(lower, strings.ToLower(pat)) {
			return Classification{
				Class:  ClassFatal,
				Reason: "non-recoverable error",
				Detail: pat,
			}
		}
	}

	for _, pat := range throttlePatterns {
		if strings.Contains(lower, strings.ToLower(pat)) {
			return Classification{
				Class:  ClassThrottle,
				Reason: "rate limited",
				Detail: pat,
			}
		}
	}

	for _, pat := range retryablePatterns {
		if strings.Contains(lower, strings.ToLower(pat)) {
			return Classification{
				Class:  ClassRetryable,
				Reason: "transient error",
				Detail: pat,
			}
		}
	}

	// Unknown errors default to retryable — we'd rather retry than give up.
	return Classification{
		Class:  ClassRetryable,
		Reason: "unknown error (defaulting to retryable)",
		Detail: "",
	}
}
