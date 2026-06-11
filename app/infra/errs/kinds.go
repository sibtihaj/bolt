// Package errs provides error classification and retry logic for bolt's
// infrastructure provisioning layer.
package errs

// ErrorKind classifies an infrastructure error so the cmd layer can route it
// to the right healing or reporting strategy.
type ErrorKind int

const (
	KindUnknown     ErrorKind = iota
	KindTransient             // throttle / service hiccup — retry automatically
	KindQuota                 // account resource limit — user must free up or request increase
	KindNameConflict          // resource name already taken — suggest alternative
	KindCapacity              // instance type / AZ out of capacity — suggest alternative
	KindBadCredential         // auth failure or expired token — re-authenticate
	KindConfig                // bad parameters — show actionable fix
)

// CloudError is implemented by every typed infrastructure error.  The cmd
// layer uses it to dispatch errors to the right heal handler without importing
// cloud-specific packages directly.
type CloudError interface {
	error
	Kind() ErrorKind
	Resource() string // human label: "VPC", "EKS cluster", "RDS instance", "S3 bucket"
}
