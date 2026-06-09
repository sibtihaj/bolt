# bolt

A command-line tool for provisioning and managing **Terraform Enterprise (TFE)** environments with a single command. Spin up a full TFE instance on Kubernetes or Docker, and tear it down just as easily.

---

## Table of Contents

- [Overview](#overview)
- [Prerequisites](#prerequisites)
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
- [Project Structure](#project-structure)
- [Runtime State](#runtime-state)
- [Config File](#config-file)
- [Examples](#examples)
  - [Local dev with Docker (disk mode)](#local-dev-with-docker-disk-mode)
  - [EKS with external storage](#eks-with-external-storage)
  - [AKS active-active](#aks-active-active)
  - [GKE with a pinned chart version](#gke-with-a-pinned-chart-version)
  - [Self-hosted kubeadm cluster](#self-hosted-kubeadm-cluster)
  - [Remote Docker host via SSH](#remote-docker-host-via-ssh)
  - [Dry run](#dry-run)

---

## Overview

bolt wraps `helm`, `kubectl`, and `docker compose` to give you a single declarative command for the full TFE lifecycle:

```
bolt deploy k8s  --name prod  --cluster-type eks  --hostname tfe.example.com  ...
bolt status      --name prod
bolt destroy     --name prod
```

**Supported deployment targets:**

| Target | Tool required |
|---|---|
| EKS (AWS) | `aws`, `kubectl`, `helm` |
| AKS (Azure) | `az`, `kubectl`, `helm` |
| GKE (GCP) | `gcloud`, `kubectl`, `helm` |
| kubeadm / self-hosted K8s | `kubectl`, `helm` |
| Docker (local or remote) | `docker` with the Compose plugin |

---

## Prerequisites

**All deployments:**
- Go 1.22+ (to build from source)
- A valid **TFE license** from HashiCorp
- A TLS certificate and private key (or use `--generate-tls` for a self-signed cert in dev)
- An encryption password

**Kubernetes deployments additionally require:**
- `kubectl` on your PATH
- `helm` v3+ on your PATH
- Cloud provider CLI for managed clusters:
  - EKS → `aws` CLI with credentials configured
  - AKS → `az` CLI, logged in or with a service principal
  - GKE → `gcloud` CLI with credentials configured

**Docker deployments additionally require:**
- `docker` with the Compose plugin (`docker compose version` must succeed)

---

## Installation

```bash
git clone https://github.com/sibtihaj/bolt.git
cd bolt
go build -o bolt .

# Optional: move to a directory on your PATH
mv bolt /usr/local/bin/bolt
```

---

## Quick Start

```bash
# Local Docker deployment (self-signed TLS, disk mode — no external services needed)
bolt deploy docker \
  --name local-tfe \
  --hostname localhost \
  --generate-tls \
  --license "$TFE_LICENSE" \
  --encryption-password "changeme123"

# Check it's running
bolt status --name local-tfe

# Connect other tools to it
eval $(bolt output --name local-tfe --format export)
# → sets TFE_ADDRESS and TFE_HOSTNAME in your shell

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

The default is `disk`. For `external` and `active-active`, bolt requires you to supply database, S3, and (where applicable) Redis connection details via flags or environment variables.

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

**Secrets are never written to disk.** The state file (`~/.bolt/deployments/<name>.json`) stores only non-secret metadata (cluster name, namespace, hostname, etc.).

---

## Cloud Provider Credentials

### How credentials are passed to bolt

bolt does **not** require you to pass cloud provider credentials as flags. Instead, it inherits whatever credentials are already present in your terminal session.

Every subprocess bolt spawns — `aws`, `az`, `gcloud`, `kubectl`, `helm` — runs as a child of your shell and automatically picks up its full environment. If you have valid credentials exported in your terminal before running bolt, they will be used transparently with no extra flags required.

This means any credential mechanism your organisation uses works out of the box:

| Mechanism | How to set it up |
|---|---|
| Static keys | `export AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=...` |
| STS / temporary credentials | `export AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=... AWS_SESSION_TOKEN=...` |
| Named profile | `export AWS_PROFILE=my-profile` (reads from `~/.aws/credentials`) |
| AWS SSO | `aws sso login` then run bolt — the session token is picked up automatically |
| `aws-vault` / `saml2aws` / corporate credential helper | `aws-vault exec my-profile -- bolt deploy k8s ...` |
| Azure service principal | `export AZURE_CLIENT_ID=... AZURE_CLIENT_SECRET=... AZURE_TENANT_ID=...` |
| Azure interactive login | `az login` before running bolt |
| GCP service account | `export GOOGLE_APPLICATION_CREDENTIALS=/path/to/sa-key.json` |
| GCP interactive login | `gcloud auth application-default login` before running bolt |

### The pattern for IP-restricted credentials

Many organisations (including corporate AWS environments) restrict credentials to specific IP addresses or VPNs. The recommended workflow is:

1. Connect to your VPN or corporate network
2. Obtain and export credentials in your terminal using whatever tool your organisation provides
3. Run `bolt deploy k8s ...` — bolt passes those credentials straight through to the underlying CLIs without inspecting, storing, or re-exporting them

```bash
# Step 1 — obtain credentials (your organisation's tooling)
eval $(your-credential-helper --profile tfe-deployer)
# or: export AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=... AWS_SESSION_TOKEN=...

# Step 2 — bolt uses them automatically, no credential flags needed
bolt deploy k8s \
  --name prod \
  --cluster-type eks \
  --eks-cluster-name my-eks-cluster \
  --eks-region us-east-1 \
  --hostname tfe.example.com \
  --license "$TFE_LICENSE" \
  --encryption-password "$TFE_ENCRYPTION_PASSWORD" \
  --generate-tls
```

### What you do and do not need to pass

| What | How to provide | Pass as a flag? |
|---|---|---|
| AWS / Azure / GCP credentials | `export` in terminal before running bolt | No — inherited automatically |
| STS session token | `export AWS_SESSION_TOKEN=...` | No — inherited automatically |
| Cluster name | Non-secret identifier | Yes — `--eks-cluster-name`, `--aks-cluster-name`, `--gke-cluster-name` |
| Region / zone | Non-secret identifier | Yes — `--eks-region`, `--gke-zone` |
| TFE license | `export TFE_LICENSE=...` or `--license` flag | Either works |
| TFE encryption password | `export TFE_ENCRYPTION_PASSWORD=...` or `--encryption-password` | Either works |

The `--aws-profile`, `--azure-client-id`, `--gcp-sa-key` flags are available for scripts or CI pipelines where you want to be explicit, but they are never required when credentials are already in the environment.

### Verifying your credentials before deploying

It is worth confirming your credentials are valid before running a full deploy:

```bash
# AWS
aws sts get-caller-identity

# Azure
az account show

# GCP
gcloud auth list
```

If any of these commands succeed, bolt will use the same credentials without any additional configuration.

---

## Commands

### deploy k8s

Deploy TFE on a Kubernetes cluster using the official HashiCorp Helm chart.

```
bolt deploy k8s [flags]
```

bolt automatically:
1. Checks that `kubectl` and `helm` are available
2. Configures kubeconfig for the cloud provider (EKS/AKS/GKE), or uses the provided `--kubeconfig` directly
3. Creates the namespace
4. Creates Kubernetes secrets (`tfe-secrets`, `tfe-tls`, `tfe-storage`)
5. Generates `~/.bolt/helm/<name>/values.yaml` from the operational mode template
6. Adds the HashiCorp Helm repo and runs `helm upgrade --install`
7. Waits for pods to become ready, then prints a health summary

**Required flags:**

| Flag | Description |
|---|---|
| `-n, --name` | Logical deployment name (used for state tracking) |
| `--cluster-type` | `eks`, `aks`, `gke`, or `kubeadm` |
| `--hostname` | TFE fully-qualified domain name (e.g. `tfe.example.com`) |

**Core flags:**

| Flag | Default | Description |
|---|---|---|
| `--mode` | `disk` | Operational mode: `disk`, `external`, `active-active` |
| `--namespace` | `tfe` | Kubernetes namespace to deploy into |
| `--kubeconfig` | `~/.kube/config` | Path to kubeconfig file |
| `--image-tag` | `latest` | TFE container image tag |
| `--helm-chart-version` | latest | Pin a specific Helm chart version |
| `--wait-timeout` | `10m` | Helm `--wait` timeout |
| `--generate-tls` | false | Generate a self-signed certificate (dev/testing only) |
| `--dry-run` | false | Render `values.yaml` and exit without deploying |

**Credential flags:**

| Flag | Env var fallback | Description |
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

**Cloud provider flags (EKS):**

| Flag | Description |
|---|---|
| `--eks-cluster-name` | EKS cluster name |
| `--eks-region` | EKS cluster AWS region |
| `--aws-profile` | AWS credentials profile name |

**Cloud provider flags (AKS):**

| Flag | Description |
|---|---|
| `--aks-cluster-name` | AKS cluster name |
| `--aks-resource-group` | Azure resource group |
| `--azure-client-id` | Service principal client ID |
| `--azure-client-secret` | Service principal client secret |
| `--azure-tenant-id` | Azure tenant ID |
| `--azure-subscription-id` | Azure subscription ID |

**Cloud provider flags (GKE):**

| Flag | Description |
|---|---|
| `--gke-cluster-name` | GKE cluster name |
| `--gke-zone` | GKE cluster zone |
| `--gke-project` | GCP project ID |
| `--gcp-sa-key` | Path to GCP service account key JSON |

---

### deploy docker

Deploy TFE using Docker Compose on a local or remote Docker host.

```
bolt deploy docker [flags]
```

bolt automatically:
1. Checks that `docker` and the Compose plugin are available
2. Creates the data directory (disk mode only)
3. Generates or validates TLS certificates
4. Writes `~/.bolt/compose/<name>/docker-compose.yaml` (secrets stay as `${VAR}` references — never in the file)
5. Runs `docker compose up --detach --wait`
6. Prints a container status summary

**Required flags:**

| Flag | Description |
|---|---|
| `-n, --name` | Logical deployment name |
| `--hostname` | TFE fully-qualified domain name |

**Core flags:**

| Flag | Default | Description |
|---|---|---|
| `--mode` | `disk` | Operational mode: `disk`, `external`, `active-active` |
| `--image-tag` | `latest` | TFE container image tag |
| `--data-dir` | `~/.bolt/data/<name>` | Host path for disk-mode data volume |
| `--wait-timeout` | `600` | `docker compose --wait-timeout` in seconds |
| `--generate-tls` | false | Generate a self-signed certificate (dev/testing only) |
| `--dry-run` | false | Render `docker-compose.yaml` and exit without starting containers |

**Remote Docker flags:**

| Flag | Description |
|---|---|
| `--ssh-host` | Deploy to a remote Docker host (e.g. `192.168.1.10`) |
| `--ssh-user` | SSH username for the remote host (default: current user) |
| `--ssh-key` | Path to SSH private key for the remote host |

**Credential flags** — same as `deploy k8s` minus the cloud-provider flags.

---

### destroy

Tear down a TFE deployment and remove its state record.

```
bolt destroy --name <name> [--force]
```

- **Kubernetes:** runs `helm uninstall`, then deletes the namespace
- **Docker:** runs `docker compose down -v` (removes containers and volumes)
- State file at `~/.bolt/deployments/<name>.json` is deleted on success

| Flag | Default | Description |
|---|---|---|
| `-n, --name` | — | Deployment name (required) |
| `-f, --force` | false | Continue destroying even if individual steps error |

---

### status

Show the current status of a deployment and live resource state.

```
bolt status --name <name>
```

Prints stored metadata (backend, mode, hostname, timestamps) then calls the underlying backend to show live resource state — `kubectl get pods` for Kubernetes or `docker compose ps` for Docker.

| Flag | Description |
|---|---|
| `-n, --name` | Deployment name (required) |

---

### list

List all deployments known to bolt.

```
bolt list [--output table|json]
```

| Flag | Default | Description |
|---|---|---|
| `-o, --output` | `table` | Output format: `table` or `json` |

**Table output example:**
```
NAME        BACKEND   MODE       STATUS    HOSTNAME             CREATED
local-tfe   docker    disk       running   localhost            2026-06-05 14:30
prod-k8s    k8s       external   running   tfe.example.com      2026-06-05 16:00
```

---

### output

Print environment variables for connecting tools to a deployment.

```
bolt output --name <name> [--format export|json]
```

| Flag | Default | Description |
|---|---|---|
| `-n, --name` | — | Deployment name (required) |
| `--format` | `export` | `export` (shell eval) or `json` |

**Usage:**
```bash
# Source into your shell
eval $(bolt output --name prod-k8s --format export)

# JSON for scripting
bolt output --name prod-k8s --format json
```

**Output variables:**

| Variable | Value |
|---|---|
| `TFE_ADDRESS` | `https://<hostname>` |
| `TFE_HOSTNAME` | The TFE hostname |

---

### version

Print the bolt version.

```
bolt version
```

---

## Project Structure

```
bolt/
├── main.go                        # Entry point — calls cmd.Execute()
├── version.go                     # Version string (set at build time)
├── go.mod / go.sum
│
├── cmd/                           # CLI layer — Cobra commands
│   ├── root.go                    # Root command, --config flag, config loading
│   ├── deploy.go                  # 'deploy' parent command
│   ├── deploy_k8s.go              # 'deploy k8s' — flags and RunE wiring
│   ├── deploy_docker.go           # 'deploy docker' — flags and RunE wiring
│   ├── destroy.go
│   ├── list.go
│   ├── status.go
│   ├── output.go
│   └── version.go
│
├── internal/
│   └── exec/
│       └── runner.go              # Run() and Output() — shared exec.Command wrapper
│
└── app/                           # Business logic
    ├── config/
    │   ├── types.go               # TFEConfig struct
    │   └── config.go              # Load / Save config file
    │
    ├── state/
    │   ├── types.go               # TFEDeployment, Backend, OperationalMode, ClusterType
    │   └── state.go               # Load / Save (atomic) / Delete / List
    │
    ├── credentials/
    │   └── resolver.go            # Resolve() — flag → env → config priority chain
    │
    ├── tls/
    │   └── selfsigned.go          # GenerateSelfSignedCert()
    │
    ├── helm/
    │   ├── helm.go                # RepoAdd / RepoUpdate / Install / Uninstall
    │   ├── values.go              # BuildValues() — renders values.yaml via text/template
    │   └── templates/
    │       ├── values-disk.yaml.tmpl
    │       ├── values-external.yaml.tmpl
    │       └── values-active-active.yaml.tmpl
    │
    ├── kubectl/
    │   └── kubectl.go             # CreateNamespace / UpsertSecret / UpsertTLSSecret / GetPods
    │
    ├── docker/
    │   ├── docker.go              # ComposeUp / ComposeDown / ComposePs
    │   ├── compose.go             # BuildCompose() / WriteCompose()
    │   └── templates/
    │       ├── compose-disk.yaml.tmpl
    │       ├── compose-external.yaml.tmpl
    │       └── compose-active-active.yaml.tmpl
    │
    ├── cloud/
    │   ├── eks.go                 # ConfigureEKSKubeconfig  (aws eks update-kubeconfig)
    │   ├── aks.go                 # ConfigureAKSKubeconfig  (az aks get-credentials)
    │   └── gke.go                 # ConfigureGKEKubeconfig  (gcloud container clusters get-credentials)
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
├── config.yaml                    # Optional global defaults
├── deployments/
│   └── <name>.json                # One file per deployment (no secrets stored here)
├── helm/
│   └── <name>/
│       └── values.yaml            # Generated Helm values for the last deploy
├── compose/
│   └── <name>/
│       └── docker-compose.yaml    # Generated compose file for the last deploy
├── tls/
│   └── <name>/
│       ├── tfe.crt                # Auto-generated cert (only when --generate-tls)
│       └── tfe.key
└── data/
    └── <name>/                    # Docker disk-mode bind-mount data
```

**State file fields** (`~/.bolt/deployments/<name>.json`):

```json
{
  "name": "local-tfe",
  "backend": "docker",
  "mode": "disk",
  "hostname": "localhost",
  "image_tag": "latest",
  "tls_cert_path": "/Users/you/.bolt/tls/local-tfe/tfe.crt",
  "tls_key_path": "/Users/you/.bolt/tls/local-tfe/tfe.key",
  "self_signed_tls": true,
  "data_dir": "/Users/you/.bolt/data/local-tfe",
  "status": "running",
  "created_at": "2026-06-05T14:30:00Z",
  "updated_at": "2026-06-05T14:45:00Z"
}
```

Secrets (license, encryption password, database URLs, S3 keys) are **never written to this file**.

---

## Config File

`~/.bolt/config.yaml` is optional. It provides defaults for values you use in every deployment so you don't have to repeat them on every command.

```yaml
# ~/.bolt/config.yaml

# Path to your TFE license file
default_license_path: /home/you/licenses/tfe.hclic

# Default encryption password (use a secrets manager for production)
default_encryption_password: ""

# Default image tag to use when --image-tag is not provided
default_image_tag: "v202501-1"

# Arbitrary additional defaults
defaults:
  some_key: some_value
```

The config file path can be overridden globally:

```bash
bolt --config /path/to/custom-config.yaml deploy docker ...
```

---

## Examples

### Local dev with Docker (disk mode)

The simplest possible deployment — everything embedded, no cloud accounts needed.

```bash
bolt deploy docker \
  --name dev \
  --hostname localhost \
  --generate-tls \
  --license "$TFE_LICENSE" \
  --encryption-password "dev-password-123"
```

After deployment:
```bash
bolt status --name dev
curl -k https://localhost/_health_check   # should return 200
bolt destroy --name dev
```

---

### EKS with external storage

```bash
bolt deploy k8s \
  --name prod \
  --cluster-type eks \
  --hostname tfe.internal.example.com \
  --eks-cluster-name my-eks-cluster \
  --eks-region us-east-1 \
  --aws-profile my-aws-profile \
  --mode external \
  --license "$TFE_LICENSE" \
  --encryption-password "$TFE_ENCRYPTION_PASSWORD" \
  --tls-cert ./certs/tfe.crt \
  --tls-key  ./certs/tfe.key \
  --db-url "postgres://tfe:password@mydb.us-east-1.rds.amazonaws.com:5432/tfe" \
  --s3-bucket my-tfe-bucket \
  --s3-region us-east-1 \
  --s3-access-key "$AWS_ACCESS_KEY_ID" \
  --s3-secret-key "$AWS_SECRET_ACCESS_KEY" \
  --namespace tfe \
  --wait-timeout 15m
```

---

### AKS active-active

```bash
bolt deploy k8s \
  --name ha-prod \
  --cluster-type aks \
  --hostname tfe.example.com \
  --aks-cluster-name my-aks-cluster \
  --aks-resource-group my-resource-group \
  --azure-client-id "$AZURE_CLIENT_ID" \
  --azure-client-secret "$AZURE_CLIENT_SECRET" \
  --azure-tenant-id "$AZURE_TENANT_ID" \
  --azure-subscription-id "$AZURE_SUBSCRIPTION_ID" \
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
  --redis-url "$TFE_REDIS_URL" \
  --wait-timeout 20m
```

---

### GKE with a pinned chart version

```bash
bolt deploy k8s \
  --name staging \
  --cluster-type gke \
  --hostname tfe-staging.example.com \
  --gke-cluster-name my-gke-cluster \
  --gke-zone us-central1-a \
  --gke-project my-gcp-project \
  --gcp-sa-key ./sa-key.json \
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

---

### Self-hosted kubeadm cluster

When `--cluster-type kubeadm` is used, bolt skips cloud kubeconfig setup and uses your local kubeconfig directly.

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

---

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

---

### Dry run

Preview the generated Helm values or Docker Compose file without deploying anything.

```bash
# Kubernetes — prints rendered values.yaml
bolt deploy k8s \
  --name smoke \
  --cluster-type kubeadm \
  --hostname tfe.example.com \
  --license fake \
  --encryption-password fake \
  --generate-tls \
  --dry-run

# Docker — prints rendered docker-compose.yaml
bolt deploy docker \
  --name smoke \
  --hostname localhost \
  --license fake \
  --encryption-password fake \
  --generate-tls \
  --dry-run
```

---

## Security Notes

- `--generate-tls` produces a **self-signed certificate** that is not trusted by browsers or external systems. Use it only for local development and testing.
- Provide production TLS certificates signed by a public or internal CA using `--tls-cert` / `--tls-key`.
- Secrets (license, passwords, keys) are resolved in memory only — they are never written to the state files or generated YAML files on disk. Docker Compose files use `${VAR}` shell-variable references; actual values are injected via the process environment at runtime.
- The state directory (`~/.bolt/`) is created with `0700` permissions; all state files and generated configs use `0600`.
