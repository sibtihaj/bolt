# Changelog

All notable changes to bolt are documented here.

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
