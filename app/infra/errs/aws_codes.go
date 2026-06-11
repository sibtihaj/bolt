package errs

import (
	"errors"

	smithy "github.com/aws/smithy-go"
)

// awsErrorCode extracts the AWS API error code from any wrapped error.
// Returns "" if err is not an AWS API error.
func awsErrorCode(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}
	return ""
}

// transientCodes are AWS error codes the SDK's own retry policy may not fully
// exhaust — we add a second layer on top.
var transientCodes = map[string]bool{
	"ThrottlingException":                true,
	"RequestLimitExceeded":               true,
	"RequestThrottled":                   true,
	"Throttling":                         true,
	"ServiceUnavailableException":        true,
	"ServiceUnavailable":                 true,
	"InternalFailure":                    true,
	"InternalServerError":                true,
	"SlowDown":                           true, // S3
	"ProvisionedThroughputExceededException": true,
	"TransactionInConflict":              true,
	"LimitExceededException":             true, // IAM (not quota — transient burst limit)
	"TooManyRequestsException":           true,
}

// quotaCodes are hard account limits — the user needs to free up resources or
// request a quota increase.
var quotaCodes = map[string]bool{
	"VpcLimitExceeded":             true,
	"SubnetLimitExceeded":          true,
	"SecurityGroupLimitExceeded":   true,
	"InternetGatewayLimitExceeded": true,
	"RouteTableLimitExceeded":      true,
	"ResourceLimitExceededException": true, // EKS
	"InstanceQuotaExceededFault":   true,   // RDS
	"DBParameterGroupQuotaExceeded": true,  // RDS
	"DBSubnetGroupQuotaExceeded":   true,   // RDS
	"StorageQuotaExceededFault":    true,   // RDS
	"TooManyBuckets":               true,   // S3
}

// nameConflictCodes indicate the requested name is already taken.
var nameConflictCodes = map[string]bool{
	"BucketAlreadyExists":           true, // S3 globally taken
	"ClusterAlreadyExistsException": true, // EKS
	"DBInstanceAlreadyExistsFault":  true, // RDS
	"EntityAlreadyExists":           true, // IAM
}

// capacityCodes indicate the instance class or AZ has no available capacity.
var capacityCodes = map[string]bool{
	"InsufficientDBInstanceCapacityFault": true, // RDS
	"InsufficientCapacityException":       true, // EC2
	"Unsupported":                         true, // EC2 instance type not supported in AZ
}

// credentialCodes are auth failures and expired tokens.
var credentialCodes = map[string]bool{
	"AuthFailure":            true,
	"InvalidClientTokenId":   true,
	"ExpiredTokenException":  true,
	"TokenRefreshRequired":   true,
	"NotAuthorized":          true,
	"UnauthorizedOperation":  true,
	"AccessDenied":           true,
	"InvalidUserID.NotFound": true,
}

// Classify returns the ErrorKind for err, inspecting the underlying AWS API
// error code if present.
func Classify(err error) ErrorKind {
	code := awsErrorCode(err)
	if code == "" {
		return KindUnknown
	}
	switch {
	case transientCodes[code]:
		return KindTransient
	case quotaCodes[code]:
		return KindQuota
	case nameConflictCodes[code]:
		return KindNameConflict
	case capacityCodes[code]:
		return KindCapacity
	case credentialCodes[code]:
		return KindBadCredential
	default:
		return KindUnknown
	}
}

// IsTransient returns true if err is a transient AWS error worth retrying.
func IsTransient(err error) bool { return Classify(err) == KindTransient }

// IsQuotaExceeded returns true if err is a hard account quota limit.
func IsQuotaExceeded(err error) bool { return Classify(err) == KindQuota }

// IsCredentialError returns true if err is an auth or token expiry failure.
func IsCredentialError(err error) bool { return Classify(err) == KindBadCredential }

// IsCapacityError returns true if err is an instance capacity shortage.
func IsCapacityError(err error) bool { return Classify(err) == KindCapacity }

// IsNameConflict returns true if err indicates a name is already taken.
func IsNameConflict(err error) bool { return Classify(err) == KindNameConflict }
