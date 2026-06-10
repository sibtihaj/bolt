# ⚡ bolt

> **Deploy Terraform Enterprise in a bolt** — provision and manage TFE environments on Kubernetes or Docker with a single command.

bolt ships with two modes of operation:

- **Interactive TUI** — run `bolt` with no arguments to launch a full-screen wizard with a gradient logo, guided forms, and a looping menu
- **Flag mode** — pass flags directly for scripting and CI: `bolt deploy k8s --name prod ...`

---

## Table of Contents

- [Interactive TUI](#interactive-tui)
- [Installation](#installation)
- [Quick Start](#quick-start)
- [Operational Modes](#operational-modes)
- [Credential Resolution](#credential-resolution)
- [Cloud Provider Credentials](#cloud-provider-credentials)
- [Commands](#commands)
  - [deploy k8s](#deploy-k8s)
  - [deploy docker](#deploy-docker)
  - [destroy](#destroy)
  - [status](#status)
  - [list](#list)
  - [output](#output)
  - [version](#version)
- [Staying Up to Date](#staying-up-to-date)
- [Project Structure](#project-structure)
- [Runtime State](#runtime-state)
- [Config File](#config-file)
- [Examples](#examples)

---

## Interactive TUI

Run `bolt` with no arguments to launch the interactive interface.

### Startup banner

A two-column banner greets you on launch. The **bolt** logo uses a left-to-right gradient — deep indigo `#4338CA` → violet `#7C3AED` → purple `#A855F7` → fuchsia `#D946EF`. The right panel shows quick-start commands for scripting.

```
╭──────────────────────────────────────────────────────────────────────────────────╮
│                                                │                                 │
│   ██████╗   ██████╗  ██╗      ████████╗        │  Getting started                │
│   ██╔══██╗ ██╔═══██╗ ██║      ╚══██╔══╝        │                                 │
│   ███████╗ ██║   ██║ ██║         ██║           │  Use the menu below, or pass    │
│   ██╔══██╗ ██║   ██║ ██║         ██║           │  flags directly for scripting:  │
│   ██████╔╝ ╚██████╔╝ ███████╗    ██║           │                                 │
│   ╚═════╝   ╚═════╝  ╚══════╝    ╚═╝           │    bolt deploy k8s  --name prod │
│                                                │    bolt deploy docker --name dev│
│   Deploy Terraform Enterprise in a bolt  v0.1.1│    bolt list                    │
│                                                │    bolt status  --name prod     │
│                                                │    bolt destroy  --name prod    │
╰──────────────────────────────────────────────────────────────────────────────────╯
```

### Main menu

After the banner, a persistent menu loops until you choose Exit or press `Ctrl+C`. Purple (`#5C4EE5`) is used for titles and active selectors; green (`#22C55E`) for confirmed selections.

```
  What would you like to do?

  ›  › Deploy Terraform Enterprise
  ›  ✗ Destroy a deployment
  ›  ≡ List deployments
  ›  ◎ Check deployment status
  ›  ← Exit
```

After every action, bolt pauses and waits for you before returning to the menu — so output from a long deploy is never scrolled away:

```
  ↵  Press Enter to return to main menu
  ──────────────────────────────────────────────────────
```

### Deploy wizard

Selecting **Deploy** opens a guided form. Groups of fields are shown or hidden based on your earlier answers — only the EKS section appears when you choose EKS, the storage section only appears for `external` and `active-active` modes, etc.

```
  Cluster type
  > EKS  — Amazon Elastic Kubernetes Service
    AKS  — Azure Kubernetes Service
    GKE  — Google Kubernetes Engine
    kubeadm  — self-managed cluster
```

A summary is shown before deployment proceeds, followed by a confirm prompt:

```
  Deployment summary

  Name:         prod
  Cluster:      eks (external)
  Hostname:     tfe.example.com
  Namespace:    tfe
  Self-signed:  false

  Proceed with deployment? [Deploy] [Cancel]
```

### List view

Deployments are shown in a colour-coded table. Status icons use green for running, amber for pending, red for failed:

```
  NAME               BACKEND   MODE          STATUS       HOSTNAME
  ──────────────────────────────────────────────────────────────────────
  prod               k8s       external      ● running    tfe.example.com
  staging            docker    disk          ◐ pending    tfe.staging.local
  old-test           k8s       disk          ✗ failed     tfe.test.local
```

### Status card

Checking status for a single deployment renders a rounded card:

```
  ╭────────────────────────────────────────────────────╮
  │                                                    │
  │   prod                                             │
  │                                                    │
  │   Backend:   k8s                                   │
  │   Mode:      external                              │
  │   Status:    ● running                             │
  │   URL:       https://tfe.example.com               │
  │   Updated:   2026-06-10 14:32:00                   │
  │                                                    │
  ╰────────────────────────────────────────────────────╯
```

### Update notice

When a newer version of bolt is available, an amber notice is displayed when you exit:

```
  ╭──────────────────────────────────────────────────────────────╮
  │  ⚡  A new version of bolt is available: v0.2.0              │
  │                                                              │
  │  Upgrade:        brew upgrade sibtihaj/tap/bolt              │
  │  Release notes:  https://github.com/sibtihaj/bolt/releases…  │
  ╰──────────────────────────────────────────────────────────────╯
```

---

## Installation

### Homebrew (recommended)

```bash
brew tap sibtihaj/tap
brew install sibtihaj/tap/bolt
```

Upgrade to the latest version at any time:

```bash
brew upgrade sibtihaj/tap/bolt
```

### Build from source

```bash
git clone https://github.com/sibtihaj/bolt.git
cd bolt
go build -o bolt .

# Optional: move to a directory on your PATH
mv bolt /usr/local/bin/bolt
```

---

## Quick Start

### Interactive (recommended for first use)

```bash
bolt          # launches the TUI wizard
```

### Flag mode (for scripting and CI)

```bash
# Local Docker deployment — self-signed TLS, disk mode, no cloud account needed
bolt deploy docker \
  --name local-tfe \
  --hostname localhost \
  --generate-tls \
  --license "$TFE_LICENSE" \
  --encryption-password "changeme123"

# Check it is running
bolt status --name local-tfe

# Tear it down
bolt destroy --name local-tfe
```

---

## Operational Modes

TFE supports three operational modes, selected with `--mode`:

| Mode | `--mode` value | PostgreSQL | Object Storage | Redis | Use case |
|---|---|---|---|---|---|
| **Disk** | `disk` | Embedded | Embedded | — | Local dev / testing |
| **External** | `external` | External (required) | External S3 (required) | — | Single-instance production |
| **Active-Active** | `active-active` | External (required) | External S3 (required) | External (required) | HA production |

The default is `disk`. For `external` and `active-active`, bolt requires database, S3, and (where applicable) Redis connection details via flags or environment variables.

---

## Credential Resolution

For every secret, bolt checks in this priority order:

```
1. CLI flag   (--license, --encryption-password, ...)
2. Environment variable  (TFE_LICENSE, TFE_ENCRYPTION_PASSWORD, ...)
3. Config file  (~/.bolt/config.yaml)
4. Error — required fields without a value abort the command
```

**Supported environment variables:**

| Variable | Used for |
|---|---|
| `TFE_LICENSE` | TFE license string |
| `TFE_LICENSE_PATH` | Path to TFE license file |
| `TFE_ENCRYPTION_PASSWORD` | TFE encryption password |
| `TFE_TLS_CERT_FILE` | Path to TLS certificate PEM |
| `TFE_TLS_KEY_FILE` | Path to TLS private key PEM |
| `TFE_DATABASE_URL` | PostgreSQL connection string |
| `TFE_S3_BUCKET` | S3 bucket name |
| `TFE_S3_REGION` | S3 bucket region |
| `TFE_S3_ACCESS_KEY_ID` | S3 access key ID |
| `TFE_S3_SECRET_ACCESS_KEY` | S3 secret access key |
| `TFE_REDIS_URL` | Redis connection URL |
| `AWS_PROFILE` | AWS credentials profile (EKS) |
| `GOOGLE_APPLICATION_CREDENTIALS` | GCP service account key path (GKE) |
| `AZURE_CLIENT_ID` | Azure service principal client ID (AKS) |
| `AZURE_CLIENT_SECRET` | Azure service principal client secret (AKS) |
| `AZURE_TENANT_ID` | Azure tenant ID (AKS) |
| `AZURE_SUBSCRIPTION_ID` | Azure subscription ID (AKS) |

**Secrets are never written to disk.** The state file (`~/.bolt/deployments/<name>.json`) stores only non-secret metadata.

---

## Cloud Provider Credentials

bolt does **not** require cloud provider credentials as flags. It inherits whatever credentials are already present in your terminal session — every subprocess (`aws`, `az`, `gcloud`, `kubectl`, `helm`) runs as a child of your shell and picks up its full environment automatically.

| Mechanism | How to set it up |
|---|---|
| Static keys | `export AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=...` |
| STS / temporary credentials | `export AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=... AWS_SESSION_TOKEN=...` |
| Named profile | `export AWS_PROFILE=my-profile` |
| AWS SSO | `aws sso login` then run bolt |
| `aws-vault` / `saml2aws` | `aws-vault exec my-profile -- bolt deploy k8s ...` |
| Azure service principal | `export AZURE_CLIENT_ID=... AZURE_CLIENT_SECRET=... AZURE_TENANT_ID=...` |
| Azure interactive login | `az login` before running bolt |
| GCP service account | `export GOOGLE_APPLICATION_CREDENTIALS=/path/to/sa-key.json` |
| GCP interactive login | `gcloud auth application-default login` before running bolt |

**Verify credentials before deploying:**

```bash
aws sts get-caller-identity   # AWS
az account show               # Azure
gcloud auth list              # GCP
```

---

## Commands

### deploy k8s

Deploy TFE on a Kubernetes cluster using the official HashiCorp Helm chart.

```
bolt deploy k8s [flags]
```

bolt automatically:
1. Checks that `kubectl` and `helm` are available
2. Configures kubeconfig for the cloud provider (EKS/AKS/GKE), or uses `--kubeconfig` directly
3. Creates the namespace and Kubernetes secrets (`tfe-secrets`, `tfe-tls`, `tfe-storage`)
4. Generates `~/.bolt/helm/<name>/values.yaml` from the operational mode template
5. Adds the HashiCorp Helm repo and runs `helm upgrade --install`
6. Waits for pods to become ready, then prints a health summary

**Required flags:**

| Flag | Description |
|---|---|
| `-n, --name` | Logical deployment name |
| `--cluster-type` | `eks`, `aks`, `gke`, or `kubeadm` |
| `--hostname` | TFE fully-qualified domain name |

**Core flags:**

| Flag | Default | Description |
|---|---|---|
| `--mode` | `disk` | Operational mode: `disk`, `external`, `active-active` |
| `--namespace` | `tfe` | Kubernetes namespace |
| `--kubeconfig` | `~/.kube/config` | Path to kubeconfig file |
| `--image-tag` | `latest` | TFE container image tag |
| `--helm-chart-version` | latest | Pin a specific Helm chart version |
| `--wait-timeout` | `10m` | Helm `--wait` timeout |
| `--generate-tls` | false | Generate a self-signed certificate (dev only) |
| `--dry-run` | false | Render `values.yaml` and exit without deploying |

**Credential flags:**

| Flag | Env var | Description |
|---|---|---|
| `--license` | `TFE_LICENSE` | TFE license string |
| `--license-path` | `TFE_LICENSE_PATH` | Path to TFE license file |
| `--encryption-password` | `TFE_ENCRYPTION_PASSWORD` | TFE encryption password |
| `--tls-cert` | `TFE_TLS_CERT_FILE` | Path to TLS certificate PEM |
| `--tls-key` | `TFE_TLS_KEY_FILE` | Path to TLS private key PEM |
| `--db-url` | `TFE_DATABASE_URL` | PostgreSQL URL (`external`/`active-active`) |
| `--s3-bucket` | `TFE_S3_BUCKET` | S3 bucket name |
| `--s3-region` | `TFE_S3_REGION` | S3 region |
| `--s3-access-key` | `TFE_S3_ACCESS_KEY_ID` | S3 access key ID |
| `--s3-secret-key` | `TFE_S3_SECRET_ACCESS_KEY` | S3 secret access key |
| `--redis-url` | `TFE_REDIS_URL` | Redis URL (`active-active` only) |

**Cloud provider flags (EKS):** `--eks-cluster-name`, `--eks-region`, `--aws-profile`

**Cloud provider flags (AKS):** `--aks-cluster-name`, `--aks-resource-group`, `--azure-client-id`, `--azure-client-secret`, `--azure-tenant-id`, `--azure-subscription-id`

**Cloud provider flags (GKE):** `--gke-cluster-name`, `--gke-zone`, `--gke-project`, `--gcp-sa-key`

---

### deploy docker

Deploy TFE using Docker Compose on a local or remote Docker host.

```
bolt deploy docker [flags]
```

**Required flags:** `-n, --name`, `--hostname`

**Core flags:**

| Flag | Default | Description |
|---|---|---|
| `--mode` | `disk` | Operational mode |
| `--image-tag` | `latest` | TFE container image tag |
| `--data-dir` | `~/.bolt/data/<name>` | Host path for disk-mode data volume |
| `--wait-timeout` | `600` | `docker compose --wait-timeout` in seconds |
| `--generate-tls` | false | Generate a self-signed certificate |
| `--dry-run` | false | Render compose file and exit |

**Remote Docker flags:** `--ssh-host`, `--ssh-user`, `--ssh-key`

**Credential flags** — same as `deploy k8s` minus cloud-provider flags.

---

### destroy

Tear down a TFE deployment and remove its state record.

```
bolt destroy --name <name> [--force]
```

- **Kubernetes:** `helm uninstall` then `kubectl delete namespace`
- **Docker:** `docker compose down -v`

---

### status

Show stored metadata and live resource state for a deployment.

```
bolt status --name <name>
```

Prints the deployment card then calls `kubectl get pods` (K8s) or `docker compose ps` (Docker).

---

### list

List all deployments known to bolt.

```
bolt list [--output table|json]
```

**Table output:**
```
  NAME               BACKEND   MODE          STATUS       HOSTNAME
  ──────────────────────────────────────────────────────────────────────
  prod               k8s       external      ● running    tfe.example.com
  local-tfe          docker    disk          ● running    localhost
```

---

### output

Print environment variables for connecting other tools to a deployment.

```
bolt output --name <name> [--format export|json]
```

```bash
eval $(bolt output --name prod --format export)
# → sets TFE_ADDRESS and TFE_HOSTNAME in your shell
```

---

### version

Print the current bolt version and check for updates.

```
bolt version
```

**Example output (up to date):**
```
bolt v0.1.1
✓  You are up to date.
```

**Example output (update available):**
```
bolt v0.1.1

  ╭──────────────────────────────────────────────────────────────╮
  │  A newer version is available: v0.2.0                        │
  │                                                              │
  │  Run: brew upgrade sibtihaj/tap/bolt                         │
  │  Release notes: https://github.com/sibtihaj/bolt/releases/…  │
  ╰──────────────────────────────────────────────────────────────╯
```

---

## Staying Up to Date

bolt checks for new releases in the background every time the TUI is opened. When a newer version is found, an amber notice is shown when you exit.

To check manually at any time:

```bash
bolt version
```

To upgrade:

```bash
brew upgrade sibtihaj/tap/bolt
```

To disable the update check (useful in CI pipelines):

```bash
export BOLT_NO_UPDATE_CHECK=1
```

Release notes for every version are published automatically at [github.com/sibtihaj/bolt/releases](https://github.com/sibtihaj/bolt/releases).

---

## Project Structure

```
bolt/
├── main.go
├── go.mod / go.sum
│
├── cmd/                           # CLI layer — Cobra commands + TUI
│   ├── root.go                    # Root command — launches TUI when called with no args
│   ├── interactive.go             # TUI: banner, main menu loop, list/status/destroy views
│   ├── interactive_deploy.go      # TUI: guided deploy wizards (K8s + Docker)
│   ├── deploy.go                  # 'deploy' parent command
│   ├── deploy_k8s.go              # 'deploy k8s' flags and RunE
│   ├── deploy_docker.go           # 'deploy docker' flags and RunE
│   ├── destroy.go
│   ├── list.go
│   ├── status.go
│   ├── output.go
│   └── version.go                 # 'bolt version' — prints version + checks for updates
│
├── internal/
│   └── exec/
│       └── runner.go              # Run() and Output() — shared exec.Command wrapper
│
└── app/                           # Business logic
    ├── config/
    │   ├── types.go
    │   └── config.go
    │
    ├── state/
    │   ├── types.go               # TFEDeployment, Backend, OperationalMode, ClusterType
    │   └── state.go               # Load / Save (atomic) / Delete / List
    │
    ├── credentials/
    │   └── resolver.go            # flag → env → config priority chain
    │
    ├── update/
    │   └── check.go               # Background GitHub release check; respects BOLT_NO_UPDATE_CHECK
    │
    ├── tls/
    │   └── selfsigned.go
    │
    ├── helm/
    │   ├── helm.go
    │   ├── values.go
    │   └── templates/
    │       ├── values-disk.yaml.tmpl
    │       ├── values-external.yaml.tmpl
    │       └── values-active-active.yaml.tmpl
    │
    ├── kubectl/
    │   └── kubectl.go
    │
    ├── docker/
    │   ├── docker.go
    │   ├── compose.go
    │   └── templates/
    │       ├── compose-disk.yaml.tmpl
    │       ├── compose-external.yaml.tmpl
    │       └── compose-active-active.yaml.tmpl
    │
    ├── cloud/
    │   ├── eks.go
    │   ├── aks.go
    │   └── gke.go
    │
    ├── retry/
    │   ├── retry.go               # Exponential backoff with equal jitter
    │   └── classifier.go          # Fatal vs retryable vs throttle error classification
    │
    ├── diagnostics/
    │   ├── k8s.go                 # kubectl warning events, pod describe, helm history
    │   ├── docker.go              # compose ps + logs on failure
    │   └── cloud.go               # CloudTrail / Azure Activity Log / GCP logging
    │
    └── tfe/
        ├── provisioner.go         # Provisioner interface + NewProvisioner factory
        ├── k8s_provisioner.go     # K8sProvisioner.Deploy / Destroy / Status
        └── docker_provisioner.go  # DockerProvisioner.Deploy / Destroy / Status
```

---

## Runtime State

bolt stores all deployment state under `~/.bolt/`:

```
~/.bolt/
├── config.yaml
├── deployments/
│   └── <name>.json                # Deployment metadata — no secrets stored here
├── helm/
│   └── <name>/values.yaml
├── compose/
│   └── <name>/docker-compose.yaml # Secrets remain as ${VAR} references
├── tls/
│   └── <name>/tfe.crt, tfe.key
└── data/
    └── <name>/                    # Docker disk-mode bind-mount
```

Secrets (license, encryption password, database URLs, S3 keys) are **never written to any file on disk**.

---

## Config File

`~/.bolt/config.yaml` provides defaults so you don't repeat common values on every command:

```yaml
# ~/.bolt/config.yaml
default_license_path: /home/you/licenses/tfe.hclic
default_encryption_password: ""
default_image_tag: "v202501-1"
```

Override the config file path globally:

```bash
bolt --config /path/to/custom-config.yaml deploy docker ...
```

---

## Examples

### Local dev with Docker (disk mode)

```bash
bolt deploy docker \
  --name dev \
  --hostname localhost \
  --generate-tls \
  --license "$TFE_LICENSE" \
  --encryption-password "dev-password-123"

bolt status --name dev
curl -k https://localhost/_health_check
bolt destroy --name dev
```

### EKS with external storage

```bash
bolt deploy k8s \
  --name prod \
  --cluster-type eks \
  --hostname tfe.internal.example.com \
  --eks-cluster-name my-eks-cluster \
  --eks-region us-east-1 \
  --mode external \
  --license "$TFE_LICENSE" \
  --encryption-password "$TFE_ENCRYPTION_PASSWORD" \
  --tls-cert ./certs/tfe.crt \
  --tls-key  ./certs/tfe.key \
  --db-url "postgres://tfe:pass@mydb.rds.amazonaws.com:5432/tfe" \
  --s3-bucket my-tfe-bucket \
  --s3-region us-east-1 \
  --s3-access-key "$AWS_ACCESS_KEY_ID" \
  --s3-secret-key "$AWS_SECRET_ACCESS_KEY"
```

### AKS active-active

```bash
bolt deploy k8s \
  --name ha-prod \
  --cluster-type aks \
  --hostname tfe.example.com \
  --aks-cluster-name my-aks-cluster \
  --aks-resource-group my-resource-group \
  --mode active-active \
  --license "$TFE_LICENSE" \
  --encryption-password "$TFE_ENCRYPTION_PASSWORD" \
  --tls-cert ./certs/tfe.crt \
  --tls-key  ./certs/tfe.key \
  --db-url "$TFE_DATABASE_URL" \
  --s3-bucket my-tfe-bucket \
  --s3-region eastus \
  --s3-access-key "$STORAGE_ACCESS_KEY" \
  --s3-secret-key "$STORAGE_SECRET_KEY" \
  --redis-url "$TFE_REDIS_URL"
```

### GKE with a pinned chart version

```bash
bolt deploy k8s \
  --name staging \
  --cluster-type gke \
  --hostname tfe-staging.example.com \
  --gke-cluster-name my-gke-cluster \
  --gke-zone us-central1-a \
  --gke-project my-gcp-project \
  --mode external \
  --license "$TFE_LICENSE" \
  --encryption-password "$TFE_ENCRYPTION_PASSWORD" \
  --tls-cert ./certs/tfe.crt \
  --tls-key  ./certs/tfe.key \
  --db-url "$TFE_DATABASE_URL" \
  --s3-bucket my-tfe-staging-bucket \
  --s3-region us-central1 \
  --s3-access-key "$GCS_ACCESS_KEY" \
  --s3-secret-key "$GCS_SECRET_KEY" \
  --helm-chart-version 1.3.0
```

### Self-hosted kubeadm cluster

```bash
bolt deploy k8s \
  --name homelab \
  --cluster-type kubeadm \
  --hostname tfe.homelab.local \
  --kubeconfig ~/.kube/homelab-config \
  --generate-tls \
  --license "$TFE_LICENSE" \
  --encryption-password "homelab-secret"
```

### Remote Docker host via SSH

```bash
bolt deploy docker \
  --name remote-dev \
  --hostname tfe.remote.example.com \
  --ssh-host 10.0.1.50 \
  --ssh-user ubuntu \
  --ssh-key ~/.ssh/id_rsa \
  --generate-tls \
  --license "$TFE_LICENSE" \
  --encryption-password "$TFE_ENCRYPTION_PASSWORD"
```

### Dry run

```bash
# Kubernetes — prints rendered values.yaml
bolt deploy k8s --name smoke --cluster-type kubeadm \
  --hostname tfe.example.com --license fake --encryption-password fake \
  --generate-tls --dry-run

# Docker — prints rendered docker-compose.yaml
bolt deploy docker --name smoke --hostname localhost \
  --license fake --encryption-password fake --generate-tls --dry-run
```

---

## Security Notes

- `--generate-tls` produces a **self-signed certificate** not trusted by browsers or external systems. Use it only for local dev and testing.
- Provide production TLS certificates signed by a public or internal CA via `--tls-cert` / `--tls-key`.
- Secrets (license, passwords, keys) are resolved in memory only — never written to state files or generated YAML on disk. Docker Compose files use `${VAR}` references; values are injected via the process environment at runtime.
- The state directory (`~/.bolt/`) is created with `0700` permissions; all state files use `0600`.
