# Plan: TFE Provisioning CLI Tool

## Context

The existing repo (`tfe-cli-tool`) is "Shikari" — a Consul Enterprise local-VM provisioner built in Go with Cobra, wrapping the Lima VM manager via `exec.Command()`. The user wants to repurpose this repo into a Terraform Enterprise (TFE) provisioning CLI that can spin up and tear down TFE environments with a single command.

The entire Lima/Consul-specific business logic (`app/shikari/`, `app/lima/`, all existing `cmd/*.go`) will be replaced with new TFE backends. The **Go + Cobra + exec.Command pattern** is kept because it works well and keeps the binary lean.

Phase 1 scope: Kubernetes (EKS, AKS, GKE, kubeadm) and Docker FDO deployments.

---

## Command Structure

```
tfe-cli
├── deploy k8s     --name --cluster-type --namespace --mode --hostname [credential flags]
├── deploy docker  --name --mode --hostname [credential flags]
├── destroy        --name [--force]
├── status         --name
├── list           [--output table|json]
└── output         --name [--format export|json]
```

### `deploy k8s` key flags
- `--name` (required) — logical name stored in state
- `--cluster-type eks|aks|gke|kubeadm` (required)
- `--namespace` (default: `tfe`)
- `--mode disk|external|active-active` (default: `disk`)
- `--hostname` (required) — TFE FQDN
- `--kubeconfig` (default: `~/.kube/config`)
- `--helm-chart-version` — pin Helm chart version
- `--image-tag` — TFE container image tag
- `--wait-timeout` (default: `10m`)
- `--generate-tls` — auto-generate self-signed cert (dev only)
- `--dry-run` — render values.yaml and exit
- Cloud-cluster flags (EKS: `--eks-cluster-name`, `--eks-region`; GKE: `--gke-cluster-name`, `--gke-zone`, `--gke-project`; AKS: `--aks-cluster-name`, `--aks-resource-group`)
- Credential flags: `--license`, `--license-path`, `--encryption-password`, `--tls-cert`, `--tls-key`, `--db-url`, `--s3-bucket`, `--s3-region`, `--s3-access-key`, `--s3-secret-key`, `--redis-url`, `--aws-profile`, `--gcp-sa-key`, `--azure-client-id`, `--azure-client-secret`, `--azure-tenant-id`, `--azure-subscription-id`

### `deploy docker` key flags
- Same credential flags as k8s
- `--data-dir` — host path for disk-mode storage (default: `~/.tfe-cli/data/<name>`)
- `--ssh-host`, `--ssh-user`, `--ssh-key` — deploy to remote Docker host (blank = local)

---

## File Layout

```
tfe-cli-tool/
├── main.go                        # unchanged
├── version.go                     # unchanged
├── go.mod                         # update module name; add gopkg.in/yaml.v3
│
├── cmd/
│   ├── root.go                    # add --config flag + cobra.OnInitialize(initConfig)
│   ├── deploy.go                  # parent 'deploy' command (no Run)
│   ├── deploy_k8s.go              # 'deploy k8s' flags + RunE wiring
│   ├── deploy_docker.go           # 'deploy docker' flags + RunE wiring
│   ├── destroy.go                 # replaces existing
│   ├── list.go                    # replaces existing
│   ├── status.go                  # new
│   └── output.go                  # new (replaces env.go)
│
├── internal/
│   └── exec/
│       └── runner.go              # Run() + Output() — shared exec.Command wrapper
│
├── app/
│   ├── config/
│   │   ├── types.go               # TFEConfig struct (yaml tags)
│   │   └── config.go              # Load(path) / Save(path, cfg)
│   │
│   ├── state/
│   │   ├── types.go               # TFEDeployment, DeploymentStatus, Backend, OperationalMode, ClusterType
│   │   └── state.go               # Load / Save (atomic rename) / Delete / List
│   │
│   ├── credentials/
│   │   └── resolver.go            # Resolve(flags, config) *TFECredentials — flag → env → config file
│   │
│   ├── tls/
│   │   └── selfsigned.go          # GenerateSelfSignedCert(hostname, certPath, keyPath)
│   │
│   ├── helm/
│   │   ├── helm.go                # RepoAdd / RepoUpdate / Install / Uninstall / Status
│   │   ├── values.go              # BuildValues(deployment, creds) string  via text/template
│   │   └── templates/
│   │       ├── values-disk.yaml.tmpl
│   │       ├── values-external.yaml.tmpl
│   │       └── values-active-active.yaml.tmpl
│   │
│   ├── kubectl/
│   │   └── kubectl.go             # CreateNamespace / CreateSecret / WaitReady / GetPods
│   │
│   ├── docker/
│   │   ├── docker.go              # ComposeUp / ComposeDown / ComposePs
│   │   ├── compose.go             # BuildCompose(deployment, creds) string  via text/template
│   │   └── templates/
│   │       ├── compose-disk.yaml.tmpl
│   │       ├── compose-external.yaml.tmpl
│   │       └── compose-active-active.yaml.tmpl
│   │
│   ├── cloud/
│   │   ├── eks.go                 # ConfigureEKSKubeconfig (runs aws eks update-kubeconfig)
│   │   ├── aks.go                 # ConfigureAKSKubeconfig (runs az aks get-credentials)
│   │   └── gke.go                 # ConfigureGKEKubeconfig (runs gcloud container clusters get-credentials)
│   │
│   └── tfe/
│       ├── provisioner.go         # Provisioner interface + NewProvisioner factory
│       ├── k8s_provisioner.go     # K8sProvisioner.Deploy / Destroy / Status
│       └── docker_provisioner.go  # DockerProvisioner.Deploy / Destroy / Status
│
└── templates/ → moved inside app/ packages (see above)
```

### State directory on disk

```
~/.tfe-cli/
├── config.yaml                    # optional defaults / credential shortcuts
├── data/<name>/                   # docker disk-mode bind-mount data
├── tls/<name>/tfe.crt, tfe.key   # auto-generated certs
├── compose/<name>/docker-compose.yaml
├── helm/<name>/values.yaml
└── deployments/<name>.json        # TFEDeployment state (no secrets written here)
```

---

## Key Data Structures

### `app/state/types.go`
```go
type TFEDeployment struct {
    Name             string          `json:"name"`
    Backend          Backend         `json:"backend"`          // "k8s" | "docker"
    Mode             OperationalMode `json:"mode"`             // "disk" | "external" | "active-active"
    ClusterType      ClusterType     `json:"cluster_type,omitempty"` // "eks"|"aks"|"gke"|"kubeadm"
    Namespace        string          `json:"namespace,omitempty"`
    Hostname         string          `json:"hostname"`
    ImageTag         string          `json:"image_tag"`
    HelmChartVersion string          `json:"helm_chart_version,omitempty"`
    Kubeconfig       string          `json:"kubeconfig,omitempty"`
    TLSCertPath      string          `json:"tls_cert_path"`
    TLSKeyPath       string          `json:"tls_key_path"`
    SelfSignedTLS    bool            `json:"self_signed_tls"`
    DataDir          string          `json:"data_dir,omitempty"`
    SSHHost          string          `json:"ssh_host,omitempty"`
    SSHUser          string          `json:"ssh_user,omitempty"`
    SSHKeyPath       string          `json:"ssh_key_path,omitempty"`
    Status           DeploymentStatus `json:"status"`
    CreatedAt        time.Time       `json:"created_at"`
    UpdatedAt        time.Time       `json:"updated_at"`
    StorageConfig    *StorageConfig  `json:"storage_config,omitempty"` // no secrets
}
// StorageConfig stores non-secret references (bucket names, regions)
// Secrets (access keys, passwords) are never persisted — resolved at runtime
```

### `app/credentials/resolver.go`
```go
// Priority: CLI flag → environment variable → config file field → error
func Resolve(flags Flags, cfg *config.TFEConfig) (*TFECredentials, error)

// TFE credential env vars:
//   TFE_LICENSE, TFE_LICENSE_PATH, TFE_ENCRYPTION_PASSWORD
//   TFE_TLS_CERT_FILE, TFE_TLS_KEY_FILE
//   TFE_DATABASE_URL, TFE_S3_BUCKET, TFE_S3_REGION
//   TFE_S3_ACCESS_KEY_ID, TFE_S3_SECRET_ACCESS_KEY, TFE_REDIS_URL
// Cloud provider env vars (standard — e.g. AWS_PROFILE, GOOGLE_APPLICATION_CREDENTIALS)
```

### `app/tfe/provisioner.go`
```go
type Provisioner interface {
    Deploy(creds *credentials.TFECredentials) error
    Destroy(force bool) error
    Status() (*ProvisionerStatus, error)
}
func NewProvisioner(d *state.TFEDeployment) (Provisioner, error)
```

---

## Kubernetes Provisioning Flow (`K8sProvisioner.Deploy`)

1. **Prerequisite check** — `kubectl version --client` and `helm version`; exit with clear error if missing
2. **Cloud kubeconfig** (skipped if explicit `--kubeconfig`):
   - EKS → `aws eks update-kubeconfig --name <cluster> --region <region>`
   - AKS → `az aks get-credentials --resource-group <rg> --name <cluster>`
   - GKE → `gcloud container clusters get-credentials <cluster> --zone <zone> --project <project>`
   - kubeadm → use `--kubeconfig` as-is
3. **Create namespace** — `kubectl create namespace <ns>` (suppress "already exists")
4. **Create secrets** (delete-then-create pattern for idempotency):
   - `tfe-secrets`: keys `TFE_LICENSE`, `TFE_ENCRYPTION_PASSWORD`
   - `tfe-tls`: keys `tls.crt`, `tls.key`
   - `tfe-storage` (external/active-active): DB URL, S3 config, Redis URL
5. **Generate values.yaml** — render template via `app/helm/values.go`, write to `~/.tfe-cli/helm/<name>/values.yaml`
6. **Helm repo** — `helm repo add hashicorp https://helm.releases.hashicorp.com && helm repo update hashicorp`
7. **Helm install** — `helm upgrade --install tfe hashicorp/terraform-enterprise --namespace <ns> --values <path> --wait --timeout <timeout>` (streams stdout/stderr live)
8. **Save state** — write `TFEDeployment{Status: Running}` to `~/.tfe-cli/deployments/<name>.json`
9. **Health summary** — `kubectl get pods -n <ns>` output + print TFE URL

## Docker Provisioning Flow (`DockerProvisioner.Deploy`)

1. **Prerequisite check** — `docker info` + `docker compose version`
2. **Remote Docker** — if `--ssh-host` set, inject `DOCKER_HOST=ssh://<user>@<host>` into all subsequent commands
3. **Data directory** (disk mode) — `os.MkdirAll(~/.tfe-cli/data/<name>/tfe)`
4. **TLS** — if `--generate-tls`: call `app/tls/selfsigned.go`; else validate cert/key files exist
5. **Generate compose.yaml** — render template, write to `~/.tfe-cli/compose/<name>/docker-compose.yaml`; secrets remain as `${VAR}` references, NOT embedded in file
6. **docker compose up** — `docker compose --file <path> --project-name tfe-<name> up --detach --wait --wait-timeout <seconds>`; inject credential env vars into `cmd.Env` (never in the file)
7. **Save state**
8. **Health summary** — `docker compose --project-name tfe-<name> ps` + TFE URL

## Destroy Flow

- K8s: `helm uninstall tfe --namespace <ns>` then `kubectl delete namespace <ns>` (with `--force` flag)
- Docker: `docker compose --project-name tfe-<name> down -v` (removes containers + volumes)
- Both: `state.Delete(name)` after successful teardown

---

## Helm Values Template Summary

Three templates, selected by `deployment.Mode`:

**disk** (single instance, embedded storage):
```yaml
replicaCount: 1
tfe:
  hostname: "{{ .Hostname }}"
  license:       { secretName: tfe-secrets, secretKey: TFE_LICENSE }
  encryptionPassword: { secretName: tfe-secrets, secretKey: TFE_ENCRYPTION_PASSWORD }
operationalMode: disk
tls:
  certData: { secretName: tfe-tls, secretKey: tls.crt }
  keyData:  { secretName: tfe-tls, secretKey: tls.key }
service:
  type: LoadBalancer
  port: 443
```

**external** extends disk, adds:
```yaml
operationalMode: external
database:      { secretName: tfe-storage, secretKey: TFE_DATABASE_URL }
objectStorage: { type: s3, bucket/region/accessKey/secretKey from tfe-storage }
```

**active-active** extends external, adds:
```yaml
operationalMode: active-active
replicaCount: {{ .ReplicaCount }}    # default 2
redis: { secretName: tfe-storage, secretKey: TFE_REDIS_URL }
```

## Docker Compose Template Summary

Three templates follow the same pattern. Secrets use `${VAR}` expansion (resolved from `cmd.Env`, never written to disk):

```yaml
services:
  tfe:
    image: "images.releases.hashicorp.com/hashicorp/terraform-enterprise:{{ .ImageTag }}"
    environment:
      TFE_LICENSE: "${TFE_LICENSE}"
      TFE_ENCRYPTION_PASSWORD: "${TFE_ENCRYPTION_PASSWORD}"
      TFE_HOSTNAME: "{{ .Hostname }}"
      TFE_OPERATIONAL_MODE: "disk"          # or external / active-active
      TFE_TLS_CERT_FILE: "/etc/ssl/private/tfe.crt"
      TFE_TLS_KEY_FILE:  "/etc/ssl/private/tfe.key"
      # external/active-active adds: TFE_DATABASE_URL, TFE_OBJECT_STORAGE_*, TFE_REDIS_URL
    volumes:
      - "{{ .DataDir }}:/var/lib/terraform-enterprise"
      - "{{ .TLSCertPath }}:/etc/ssl/private/tfe.crt:ro"
      - "{{ .TLSKeyPath }}:/etc/ssl/private/tfe.key:ro"
    cap_add: [IPC_LOCK]
    healthcheck:
      test: ["CMD", "curl", "-kf", "https://localhost/_health_check"]
      interval: 30s
      retries: 10
      start_period: 120s
```

---

## `internal/exec/runner.go` Pattern

Replace the existing scattered `exec.Command()` calls with two functions:

```go
// Run — streams stdout/stderr live (for helm install, docker compose up, etc.)
func Run(name string, args []string, opts RunOptions) error

// Output — captures stdout (for parsing JSON/status output)
func Output(name string, args []string, opts RunOptions) ([]byte, error)

type RunOptions struct {
    Stdout io.Writer  // defaults to os.Stdout
    Stderr io.Writer  // defaults to os.Stderr
    Env    []string   // appended to os.Environ()
    Dir    string
}
```

Reference: [app/lima/lima.go](app/lima/lima.go) for the existing exec.Command wrapping pattern.

---

## go.mod Changes

- Rename module from `github.com/ranjandas/shikari` to the user's org path
- Add one new direct dependency: `gopkg.in/yaml.v3` (for `config.yaml` parsing; already a transitive dep)
- Everything else (JSON, text/template, crypto/*) is stdlib

---

## Patterns to Reuse from Existing Code

| What | From (existing) | To (new) |
|------|----------------|----------|
| Package-level opts struct + `init()` flag wiring | [cmd/scale.go](cmd/scale.go) | `cmd/deploy_k8s.go`, `cmd/deploy_docker.go` |
| goroutine + WaitGroup + errCh concurrency | [cmd/destroy.go](cmd/destroy.go) | `cmd/destroy.go` (teardown multiple k8s resources in parallel) |
| exec.Command wrapping pattern | [app/lima/lima.go](app/lima/lima.go) | `internal/exec/runner.go` → all backends |
| Top-level domain struct | [app/shikari/types.go](app/shikari/types.go) | `app/state/types.go` (TFEDeployment) |
| Root init + version | [cmd/root.go](cmd/root.go), [version.go](version.go) | Keep as-is, add config init |

---

## Implementation Order

1. `internal/exec/runner.go` — no deps
2. `app/state/types.go` + `app/state/state.go` — stdlib only
3. `app/config/types.go` + `app/config/config.go` — adds yaml.v3
4. `app/credentials/resolver.go`
5. `app/tls/selfsigned.go`
6. Helm templates + `app/helm/values.go`
7. `app/kubectl/kubectl.go` + `app/helm/helm.go`
8. `app/cloud/eks.go`, `aks.go`, `gke.go`
9. `app/tfe/k8s_provisioner.go`
10. Docker templates + `app/docker/compose.go`
11. `app/docker/docker.go`
12. `app/tfe/docker_provisioner.go`
13. `app/tfe/provisioner.go` factory
14. `cmd/root.go` (add config init)
15. All new `cmd/` files

---

## Verification

**Fast smoke test (no cluster):**
```bash
tfe-cli deploy k8s --name smoke --cluster-type kubeadm \
  --hostname tfe.example.com --license fake --encryption-password fake \
  --tls-cert /dev/null --tls-key /dev/null --dry-run
# Expect: prints rendered values.yaml, exits 0
```

**Docker disk mode (any machine with Docker):**
```bash
tfe-cli deploy docker --name test --mode disk \
  --hostname localhost --generate-tls \
  --license "$TFE_LICENSE" --encryption-password changeme \
  --wait-timeout 15m
curl -k https://localhost/_health_check   # expect 200
tfe-cli status --name test
tfe-cli destroy --name test --force
tfe-cli list   # expect empty
```

**K8s kubeadm mode (requires existing cluster):**
```bash
tfe-cli deploy k8s --name k8s-test --cluster-type kubeadm \
  --namespace tfe --hostname tfe.local --generate-tls \
  --license "$TFE_LICENSE" --encryption-password changeme \
  --wait-timeout 15m
kubectl get pods -n tfe
tfe-cli status --name k8s-test
tfe-cli destroy --name k8s-test
```

**Idempotency:** run `deploy` twice with the same `--name` — second run must succeed (Helm `upgrade --install` and `docker compose up` are both idempotent).

---

## TFE Docs References

- [Deploy to Kubernetes](https://developer.hashicorp.com/terraform/enterprise/deploy/kubernetes)
- [Deploy to Docker](https://developer.hashicorp.com/terraform/enterprise/flexible-deployments-beta/install/docker)
- [Operational modes](https://developer.hashicorp.com/terraform/enterprise/deploy/configuration/storage/configure-mode)
- [Configuration reference](https://developer.hashicorp.com/terraform/enterprise/deploy/reference/configuration)
- [Helm chart GitHub](https://github.com/hashicorp/terraform-enterprise-helm)
- [TLS requirements](https://developer.hashicorp.com/terraform/enterprise/deploy/replicated/requirements/credentials)
- [PostgreSQL requirements](https://developer.hashicorp.com/terraform/enterprise/deploy/replicated/requirements/data-storage/postgres-requirements)
