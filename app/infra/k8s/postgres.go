// Package k8s provides in-cluster Kubernetes resource provisioning for bolt.
// Phase 2: In-cluster PostgreSQL via StatefulSet.
package k8s

import (
	"fmt"
	"strings"

	runner "github.com/sibtihaj/bolt/internal/exec"
)

const (
	postgresImage    = "postgres:15-alpine"
	postgresUser     = "tfe"
	postgresDB       = "tfe"
	defaultNamespace = "tfe"
)

// InClusterPostgresConfig holds parameters for the in-cluster PostgreSQL pod.
type InClusterPostgresConfig struct {
	Namespace  string
	Password   string
	StorageGB  int
	Kubeconfig string
}

// EnsureInClusterPostgres applies a PostgreSQL StatefulSet + Service into the
// cluster and waits for the pod to be ready.
// Returns the in-cluster postgres:// URL.
func EnsureInClusterPostgres(cfg *InClusterPostgresConfig) (string, error) {
	ns := cfg.Namespace
	if ns == "" {
		ns = defaultNamespace
	}
	storageGB := cfg.StorageGB
	if storageGB == 0 {
		storageGB = 50
	}

	manifest := buildPostgresManifest(ns, cfg.Password, storageGB)

	opts := runner.RunOptions{}
	if cfg.Kubeconfig != "" {
		opts.Env = []string{"KUBECONFIG=" + cfg.Kubeconfig}
	}

	// Apply via kubectl apply -f - (stdin)
	applyArgs := []string{"apply", "-f", "-"}
	if cfg.Kubeconfig != "" {
		applyArgs = append(applyArgs, "--kubeconfig", cfg.Kubeconfig)
	}

	if err := runner.RunWithStdin("kubectl", applyArgs, strings.NewReader(manifest), opts); err != nil {
		return "", fmt.Errorf("applying PostgreSQL manifest: %w", err)
	}

	// Wait for the StatefulSet pod to be ready.
	waitArgs := []string{
		"rollout", "status", "statefulset/bolt-postgres",
		"-n", ns,
		"--timeout=10m",
	}
	if cfg.Kubeconfig != "" {
		waitArgs = append(waitArgs, "--kubeconfig", cfg.Kubeconfig)
	}
	if err := runner.Run("kubectl", waitArgs, opts); err != nil {
		return "", fmt.Errorf("waiting for PostgreSQL to be ready: %w", err)
	}

	return fmt.Sprintf("postgres://%s:%s@bolt-postgres.%s.svc.cluster.local:5432/%s",
		postgresUser, cfg.Password, ns, postgresDB), nil
}

// DeleteInClusterPostgres removes the PostgreSQL StatefulSet and PVC.
func DeleteInClusterPostgres(namespace, kubeconfig string) error {
	if namespace == "" {
		namespace = defaultNamespace
	}
	opts := runner.RunOptions{}
	if kubeconfig != "" {
		opts.Env = []string{"KUBECONFIG=" + kubeconfig}
	}
	args := []string{
		"delete", "statefulset,service,pvc",
		"-l", "app=bolt-postgres",
		"-n", namespace,
		"--ignore-not-found",
	}
	if kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}
	return runner.Run("kubectl", args, opts)
}

func buildPostgresManifest(namespace, password string, storageGB int) string {
	return fmt.Sprintf(`---
apiVersion: v1
kind: Service
metadata:
  name: bolt-postgres
  namespace: %s
  labels:
    app: bolt-postgres
spec:
  selector:
    app: bolt-postgres
  ports:
  - port: 5432
    targetPort: 5432
  clusterIP: None
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: bolt-postgres
  namespace: %s
  labels:
    app: bolt-postgres
spec:
  serviceName: bolt-postgres
  replicas: 1
  selector:
    matchLabels:
      app: bolt-postgres
  template:
    metadata:
      labels:
        app: bolt-postgres
    spec:
      securityContext:
        runAsUser: 999
        fsGroup: 999
      containers:
      - name: postgres
        image: %s
        ports:
        - containerPort: 5432
        env:
        - name: POSTGRES_DB
          value: %s
        - name: POSTGRES_USER
          value: %s
        - name: POSTGRES_PASSWORD
          value: %q
        - name: PGDATA
          value: /var/lib/postgresql/data/pgdata
        resources:
          requests:
            cpu: "500m"
            memory: "512Mi"
          limits:
            cpu: "2000m"
            memory: "2Gi"
        readinessProbe:
          exec:
            command: ["pg_isready", "-U", "%s"]
          initialDelaySeconds: 10
          periodSeconds: 5
        volumeMounts:
        - name: postgres-data
          mountPath: /var/lib/postgresql/data
  volumeClaimTemplates:
  - metadata:
      name: postgres-data
      labels:
        app: bolt-postgres
    spec:
      accessModes: ["ReadWriteOnce"]
      resources:
        requests:
          storage: %dGi
`,
		namespace,
		namespace,
		postgresImage,
		postgresDB,
		postgresUser,
		password,
		postgresUser,
		storageGB,
	)
}
