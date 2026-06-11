# Changelog

All notable changes to bolt are documented here.

---

## [v0.3.0] — 2026-06-11

### Smart Auto-Healing

bolt now recovers from cloud provisioning errors automatically. Where a fix can be applied silently, it is. Where human input is needed, a guided picker loops until the deployment succeeds — no more "exit, fix, rerun" cycles.

| Error condition | Recovery behaviour |
|---|---|
| **S3 bucket name taken globally** | Silently retries `{name}-tfe-a` → `-b` → … → `-z`. Falls back to an interactive name prompt only when all 26 are also taken. |
| **VPC limit reached** | Lists existing VPCs. Lets you adopt one (validates EKS suitability) or delete one to free a slot, then retries. Re-presents the picker on validation failure instead of dropping to the main menu. |
| **EKS cluster name conflict** | Detects clusters not owned by bolt. Offers: deploy on the existing cluster (writes kubeconfig only) or destroy it and provision fresh. |
| **EKS cluster quota exceeded** | Lists existing clusters. Lets you delete one to free quota, then retries. |
| **RDS instance quota exceeded** | Lists existing instances. Lets you delete one to free quota, then retries. |
| **RDS capacity unavailable** | Suggests equivalent instance classes with available capacity. Loops if the chosen alternative is also unavailable. |
| **AWS credentials expired mid-run** | Prompts for re-authentication (static keys or Doormat SSO). Resumes from where it left off — already-provisioned resources are detected and skipped. |

### VPC Capacity Validation

Before adopting an existing VPC for EKS, bolt now validates it is actually suitable: ≥2 availability zones, sufficient free IPs `(nodeCount×30)+30`, internet gateway attached, and correct route tables for public/private subnets. Failures loop back to the VPC picker with the specific reason shown.

### Live Progress Updates

EKS (~15 min) and RDS (~10 min) provisioning now print a status line every 60 seconds:

```
  ⠙  Provisioning EKS cluster (≈15 min)…
  ⋯  EKS cluster bolt-prod — AWS status: CREATING  [2m0s elapsed]
     Haven't frozen — AWS is building your infrastructure
  ✓  EKS cluster bolt-prod complete  (14m32s)
```

The status string (`CREATING`, `ACTIVE`, etc.) is fetched live from the AWS API on each tick.

### Animated Spinner

A braille `⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏` spinner now runs during every blocking operation — VPC, IAM, S3, EKS, RDS — so the terminal never appears frozen. Status updates from long operations print above the spinner line cleanly. Falls back to plain `⋯` lines when stdout is not a TTY (CI, piped output).

### Doormat Integration

HashiCorp Doormat SSO is now a first-class AWS credential source in the interactive TUI. When selected, bolt runs `doormat login` if the session is stale, lists available IAM roles via `doormat aws list`, and obtains short-lived STS credentials via `doormat aws json`. The credential expiry time is shown after authentication.

### EKS Cluster Ownership Tagging

Clusters created by bolt are tagged `bolt:deployment: <name>`. On subsequent runs, bolt uses this tag to distinguish its own clusters (silently adopted for idempotent retry) from externally-created ones (triggers the conflict prompt).

### Bug Fixes

- S3 idempotent retry: when a previous run created `{prefix}-tfe-b`, reruns now reuse that exact bucket name via `infraState.S3BucketCreated` instead of re-colliding on `{prefix}-tfe`.
- `healS3Conflict` (interactive fallback) now sets `S3BucketOverride` instead of mutating `NamePrefix`, preventing downstream resources from inheriting a corrupted prefix.

### Internal

- New `app/infra/errs/` package: `ErrorKind` taxonomy, AWS error-code classifier (~40 codes), and exponential-backoff `Do()` retry helper.
- New `app/infra/spinner.go`: thread-safe braille spinner with TTY detection and mutex-coordinated concurrent output.
- New `cmd/heal_aws.go`: all heal handlers + central dispatch `handleAWSProvisionError`.
- New `app/preflight/doormat.go`: Doormat CLI wrappers.

---

## [v0.2.0] — 2026-06-11

### Full-Stack Infrastructure Provisioner

Bolt can now provision the underlying cloud infrastructure before deploying TFE — VPC, Kubernetes cluster, object storage, and PostgreSQL — all from a single interactive wizard. No Terraform, no separate tooling.

#### New: Cloud Infrastructure Provisioning

**Three provision modes** (selected interactively at deploy time):
- `all` — bolt creates VPC, cluster, database, and object storage from scratch
- `storage-only` — bring your own cluster; bolt provisions the database and storage
- `byo` — existing behavior; supply all connection details yourself

**AWS**
- VPC: 10.0.0.0/16, 2 AZs, public + private subnets, Internet Gateway, route tables, security group
- EKS managed cluster with IAM roles and a managed node group; kubeconfig written to `~/.bolt/kubeconfigs/<name>.yaml`
- RDS PostgreSQL 15 with a random secure password; waits for `available` before continuing
- S3 bucket with AES-256 encryption, versioning, and public access blocked

**Azure** (via Azure REST API — no SDK dependency)
- AKS cluster
- Azure DB for PostgreSQL Flexible Server; polls until `Ready`
- Azure Blob Storage account (HTTPS-only) with container

**GCP** (via GCP REST API — no SDK dependency)
- GKE cluster
- Cloud SQL PostgreSQL 15
- GCS bucket with uniform IAM and versioning

**In-cluster PostgreSQL** (any cloud, any cluster)
- Deploys a PostgreSQL 15 StatefulSet via `kubectl apply` as an alternative to a managed database

#### New: Cloud Credential Validation

Credentials are validated interactively before any resource is created:
- **AWS**: STS `GetCallerIdentity` with support for AssumeRole, static keys, and ambient credentials
- **Azure**: OAuth2 client credentials grant against the Azure management API
- **GCP**: RSA-SHA256 signed JWT grant using the service account key file

Credentials are **never persisted to disk** at any point. The state file records only resource IDs.

#### New: Infrastructure Teardown (`bolt destroy`)

`bolt destroy` now tears down bolt-provisioned cloud resources before removing the TFE stack:
- Displays a full list of resources to be deleted before taking action
- Asks for confirmation
- Re-collects cloud credentials (since they are never stored)
- Deletes in reverse-dependency order: in-cluster Postgres → managed DB → object storage → cluster → VPC
- Best-effort: collects all errors and reports them together rather than stopping at the first failure
- `--force` continues through infra teardown errors to still remove the TFE layer

#### New: Interactive Infra Wizard

The `bolt deploy` TUI now includes a pre-deploy infrastructure wizard:
- Cloud provider selection (AWS / Azure / GCP)
- Provision mode selection
- Sizing tier (minimum / recommended / custom)
- Database choice (managed / in-cluster / bring your own)
- Credential entry with live validation
- Plan summary before provisioning begins

#### Other Changes

- **New color theme**: lightning gold → pale blue → bright cyan → teal, replacing the previous purple/violet palette
- **`bolt destroy`** re-prompts for credentials at teardown time and shows a typed resource plan before acting
- **Partial provisioning safety**: state is saved after each resource is created, so a mid-run failure leaves an accurate record for cleanup

#### Dependencies Added

- `github.com/aws/aws-sdk-go-v2` (modular): `config`, `credentials`, `ec2`, `eks`, `iam`, `rds`, `s3`, `sts`
- Azure and GCP integrations use stdlib `net/http` and `crypto/rsa` — no additional SDK dependencies

---

## [v0.1.1] — prior release

- Background update checker: notifies users of new releases on startup
- README: interactive TUI section, Homebrew install instructions

## [v0.1.0] — prior release

- Initial release: `bolt deploy k8s`, `bolt deploy docker`, `bolt destroy`, `bolt status`, `bolt list`, `bolt output`
- Interactive TUI mode with Charm `huh` + `lipgloss`
- Helm + Docker Compose backends
- Intelligent retry logic and failure diagnostics
- GoReleaser + GitHub Actions release pipeline
- Homebrew tap
