package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/sibtihaj/bolt/app/credentials"
	"github.com/sibtihaj/bolt/app/infra"
	"github.com/sibtihaj/bolt/app/state"
	"github.com/sibtihaj/bolt/app/tfe"
	apptls "github.com/sibtihaj/bolt/app/tls"
)

// interactiveDeploy asks which backend then routes to the right wizard.
func interactiveDeploy() error {
	var backend string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Which backend?").
				Options(
					huh.NewOption("Kubernetes  (EKS, AKS, GKE, kubeadm)", "k8s"),
					huh.NewOption("Docker  (local or remote via SSH)", "docker"),
				).
				Value(&backend),
		),
	).WithTheme(boltTheme()).Run()

	if errors.Is(err, huh.ErrUserAborted) {
		return nil
	}
	if err != nil {
		return err
	}

	switch backend {
	case "k8s":
		return interactiveDeployK8s()
	case "docker":
		return interactiveDeployDocker()
	}
	return nil
}

// ── Kubernetes deploy wizard ───────────────────────────────────────────────────
//
// New flow (Phase 1+):
//   0. Infrastructure wizard  (infra source → credentials → sizing → DB choice)
//   1. Basic info (name, cluster type, namespace, hostname, mode, TLS)
//   2–5. Cloud-specific cluster fields  (skipped when bolt provisioned the cluster)
//   6. Core TFE credentials (license, encryption password)
//   7. TLS cert+key paths (skipped when generate-TLS is chosen)
//   8. External storage details (skipped when bolt provisioned storage)
//   9. Redis URL (active-active only)

func interactiveDeployK8s() error {
	// ── Step 0: quick name capture so we can label the infra wizard ──────────
	var name string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Deployment name").
				Placeholder("prod-tfe").
				Value(&name).
				Validate(notEmpty("deployment name")),
		),
	).WithTheme(boltTheme()).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		fmt.Println("Cancelled.")
		return nil
	}
	if err != nil {
		return err
	}

	// ── Step 1: Infrastructure wizard ─────────────────────────────────────────
	infraResult, err := RunInfraWizard(name)
	if err != nil {
		if err.Error() == "cancelled" {
			fmt.Println("Deployment cancelled.")
			return nil
		}
		return err
	}

	// Determine whether bolt will provision cluster / storage.
	boltProvidesCluster := infraResult.Mode == infra.ProvisionAll
	boltProvidesStorage := infraResult.Mode == infra.ProvisionAll || infraResult.Mode == infra.ProvisionStorageOnly
	boltProvidesDB := boltProvidesStorage && infraResult.Database != infra.DBBYO

	// ── Step 2: TFE-specific settings ─────────────────────────────────────────
	var (
		clusterType string
		namespace   = "tfe"
		hostname    = os.Getenv("TFE_HOSTNAME")
		mode        = "disk"
		generateTLS bool

		eksClusterName   string
		eksRegion        string
		aksClusterName   string
		aksResourceGroup string
		gkeClusterName   string
		gkeZone          string
		gkeProject       string
		kubeconfig       string

		license            = os.Getenv("TFE_LICENSE")
		encryptionPassword = os.Getenv("TFE_ENCRYPTION_PASSWORD")
		tlsCertPath        string
		tlsKeyPath         string

		databaseURL       = os.Getenv("TFE_DATABASE_URL")
		s3Bucket          = os.Getenv("TFE_S3_BUCKET")
		s3Region          = os.Getenv("TFE_S3_REGION")
		s3AccessKeyID     = os.Getenv("TFE_S3_ACCESS_KEY_ID")
		s3SecretAccessKey = os.Getenv("TFE_S3_SECRET_ACCESS_KEY")
		redisURL          = os.Getenv("TFE_REDIS_URL")
	)

	// Pre-select cluster type from infra wizard cloud choice.
	switch infraResult.Cloud {
	case infra.CloudAWS:
		clusterType = "eks"
	case infra.CloudAzure:
		clusterType = "aks"
	case infra.CloudGCP:
		clusterType = "gke"
	case infra.CloudLocal:
		if infraResult.LocalCreds != nil && infraResult.LocalCreds.SubType == infra.LocalKubeadm {
			clusterType = "kubeadm"
		} else {
			clusterType = "kind"
		}
	}

	// If bolt provisioned storage, default to external mode.
	if boltProvidesStorage {
		mode = "external"
	}

	groups := []*huh.Group{
		// ── Basic cluster settings ─────────────────────────────────────────────
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Cluster type").
				Options(
					huh.NewOption("EKS  — Amazon Elastic Kubernetes Service", "eks"),
					huh.NewOption("AKS  — Azure Kubernetes Service", "aks"),
					huh.NewOption("GKE  — Google Kubernetes Engine", "gke"),
					huh.NewOption("kubeadm  — self-managed cluster", "kubeadm"),
					huh.NewOption("kind  — local Docker cluster", "kind"),
				).
				Value(&clusterType),
			huh.NewInput().
				Title("Kubernetes namespace").
				Placeholder("tfe").
				Value(&namespace),
			huh.NewInput().
				Title("Hostname (FQDN)").
				Placeholder("tfe.example.com").
				Value(&hostname).
				Validate(notEmpty("hostname")),
			huh.NewSelect[string]().
				Title("Operational mode").
				Options(
					huh.NewOption("Disk  — embedded storage, single node", "disk"),
					huh.NewOption("External  — PostgreSQL + S3", "external"),
					huh.NewOption("Active-Active  — PostgreSQL + S3 + Redis", "active-active"),
				).
				Value(&mode),
			huh.NewConfirm().
				Title("Generate self-signed TLS certificate?").
				Description("For dev/test only — not for production").
				Value(&generateTLS),
		),

		// ── EKS details (hidden when bolt provisioned the cluster) ─────────────
		huh.NewGroup(
			huh.NewInput().Title("EKS cluster name").Value(&eksClusterName).
				Validate(notEmpty("EKS cluster name")),
			huh.NewInput().Title("EKS region").Placeholder("us-east-1").Value(&eksRegion).
				Validate(notEmpty("EKS region")),
		).WithHideFunc(func() bool { return clusterType != "eks" || boltProvidesCluster }),

		// ── AKS details ────────────────────────────────────────────────────────
		huh.NewGroup(
			huh.NewInput().Title("AKS cluster name").Value(&aksClusterName).
				Validate(notEmpty("AKS cluster name")),
			huh.NewInput().Title("AKS resource group").Value(&aksResourceGroup).
				Validate(notEmpty("AKS resource group")),
		).WithHideFunc(func() bool { return clusterType != "aks" || boltProvidesCluster }),

		// ── GKE details ────────────────────────────────────────────────────────
		huh.NewGroup(
			huh.NewInput().Title("GKE cluster name").Value(&gkeClusterName).
				Validate(notEmpty("GKE cluster name")),
			huh.NewInput().Title("GKE zone").Placeholder("us-central1-a").Value(&gkeZone).
				Validate(notEmpty("GKE zone")),
			huh.NewInput().Title("GCP project").Value(&gkeProject).
				Validate(notEmpty("GCP project")),
		).WithHideFunc(func() bool { return clusterType != "gke" || boltProvidesCluster }),

		// ── kubeadm ────────────────────────────────────────────────────────────
		huh.NewGroup(
			huh.NewInput().
				Title("Path to kubeconfig").
				Placeholder("~/.kube/config").
				Value(&kubeconfig),
		).WithHideFunc(func() bool { return clusterType != "kubeadm" }),

		// ── Core TFE credentials ───────────────────────────────────────────────
		huh.NewGroup(
			huh.NewInput().
				Title("TFE License").
				Description("Your Terraform Enterprise license key").
				EchoMode(huh.EchoModePassword).
				Value(&license).
				Validate(notEmpty("license")),
			huh.NewInput().
				Title("Encryption password").
				EchoMode(huh.EchoModePassword).
				Value(&encryptionPassword).
				Validate(notEmpty("encryption password")),
		),

		// ── TLS cert+key (skipped when generate-TLS) ──────────────────────────
		huh.NewGroup(
			huh.NewInput().
				Title("TLS certificate path").
				Placeholder("/path/to/tfe.crt").
				Value(&tlsCertPath).
				Validate(notEmpty("TLS certificate path")),
			huh.NewInput().
				Title("TLS key path").
				Placeholder("/path/to/tfe.key").
				Value(&tlsKeyPath).
				Validate(notEmpty("TLS key path")),
		).WithHideFunc(func() bool { return generateTLS }),

		// ── External storage (skipped for disk mode OR bolt provisioned) ───────
		huh.NewGroup(
			huh.NewInput().
				Title("PostgreSQL database URL").
				Placeholder("postgres://user:pass@host:5432/tfe").
				EchoMode(huh.EchoModePassword).
				Value(&databaseURL).
				Validate(notEmpty("database URL")),
			huh.NewInput().
				Title("S3 bucket name").
				Value(&s3Bucket).
				Validate(notEmpty("S3 bucket")),
			huh.NewInput().
				Title("S3 region").
				Placeholder("us-east-1").
				Value(&s3Region).
				Validate(notEmpty("S3 region")),
			huh.NewInput().
				Title("S3 access key ID").
				Value(&s3AccessKeyID).
				Validate(notEmpty("S3 access key ID")),
			huh.NewInput().
				Title("S3 secret access key").
				EchoMode(huh.EchoModePassword).
				Value(&s3SecretAccessKey).
				Validate(notEmpty("S3 secret access key")),
		).WithHideFunc(func() bool {
			return mode == "disk" || mode == "" || boltProvidesDB
		}),

		// ── Redis (active-active only) ─────────────────────────────────────────
		huh.NewGroup(
			huh.NewInput().
				Title("Redis URL").
				Placeholder("redis://host:6379").
				Value(&redisURL).
				Validate(notEmpty("Redis URL")),
		).WithHideFunc(func() bool { return mode != "active-active" }),
	}

	err = huh.NewForm(groups...).WithTheme(boltTheme()).Run()

	if errors.Is(err, huh.ErrUserAborted) {
		fmt.Println("Cancelled.")
		return nil
	}
	if err != nil {
		return err
	}

	hostname = normalizeHostname(hostname)

	if namespace == "" {
		namespace = "tfe"
	}

	home, _ := os.UserHomeDir()

	if generateTLS {
		tlsDir := filepath.Join(home, ".bolt", "tls", name)
		tlsCertPath = filepath.Join(tlsDir, "tfe.crt")
		tlsKeyPath = filepath.Join(tlsDir, "tfe.key")
		if _, statErr := os.Stat(tlsCertPath); os.IsNotExist(statErr) {
			fmt.Print("\n" + hintStyle.Render("  Generating self-signed TLS certificate…  "))
			if err := apptls.GenerateSelfSignedCert(hostname, tlsCertPath, tlsKeyPath); err != nil {
				return fmt.Errorf("generate TLS cert: %w", err)
			}
			fmt.Println(lipgloss.NewStyle().Foreground(greenColor).Render("✓"))
		}
	}

	resolvedKubeconfig := ""
	if clusterType == "kubeadm" {
		resolvedKubeconfig = kubeconfig
		if resolvedKubeconfig == "" {
			resolvedKubeconfig = filepath.Join(home, ".kube", "config")
		}
	}

	d := &state.TFEDeployment{
		Name:             name,
		Backend:          state.BackendK8s,
		Mode:             state.OperationalMode(mode),
		ClusterType:      state.ClusterType(clusterType),
		Namespace:        namespace,
		Hostname:         hostname,
		ImageTag:         "latest",
		Kubeconfig:       resolvedKubeconfig,
		TLSCertPath:      tlsCertPath,
		TLSKeyPath:       tlsKeyPath,
		SelfSignedTLS:    generateTLS,
		EKSClusterName:   eksClusterName,
		EKSRegion:        eksRegion,
		AKSClusterName:   aksClusterName,
		AKSResourceGroup: aksResourceGroup,
		GKEClusterName:   gkeClusterName,
		GKEZone:          gkeZone,
		GKEProject:       gkeProject,
		Status:           state.StatusPending,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	infraStateRec := &state.InfraState{
		ProvisionMode:  state.ProvisionMode(infraResult.Mode),
		Cloud:          string(infraResult.Cloud),
		DatabaseChoice: state.DatabaseChoice(infraResult.Database),
	}
	if infraResult.Mode != infra.ProvisionBYO {
		d.InfraState = infraStateRec
	}

	if s3Bucket != "" {
		d.StorageConfig = &state.StorageConfig{S3Bucket: s3Bucket, S3Region: s3Region}
	}

	creds := &credentials.TFECredentials{
		License:            license,
		EncryptionPassword: encryptionPassword,
		TLSCert:            tlsCertPath,
		TLSKey:             tlsKeyPath,
		DatabaseURL:        databaseURL,
		S3Bucket:           s3Bucket,
		S3Region:           s3Region,
		S3AccessKeyID:      s3AccessKeyID,
		S3SecretAccessKey:  s3SecretAccessKey,
		RedisURL:           redisURL,
	}

	// ── Phase 2+ : provision infrastructure if bolt is managing it ────────────
	if infraResult.Mode != infra.ProvisionBYO {
		infraCfg := buildInfraConfig(infraResult, name)
		fmt.Println()
		fmt.Println(sectionStyle.Render("  Provisioning infrastructure"))
		fmt.Println(hintStyle.Render("  This may take 15–30 minutes for a full cluster provisioning."))
		fmt.Println()

		var outputs *infra.InfraOutputs
		for {
			var provErr error
			outputs, provErr = infra.Provision(context.Background(), infraCfg, infraStateRec)
			if provErr != nil {
				outputs, provErr = handleAWSProvisionError(context.Background(), provErr, infraCfg, infraStateRec)
			}
			if provErr == nil {
				break
			}

			fmt.Println()
			fmt.Println(errorBoxStyle.Render("  ✗  " + provErr.Error()))
			fmt.Println()
			fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(amberColor).Render("  Suggestion:"))
			fmt.Println(hintStyle.Render("  " + infraHint(provErr)))
			fmt.Println()

			var choice string
			formErr := huh.NewForm(
				huh.NewGroup(
					huh.NewSelect[string]().
						Title("What would you like to do?").
						Description("Already-completed steps are skipped on retry.").
						Options(
							huh.NewOption("  ↺  Retry  (after fixing the issue above)", "retry"),
							huh.NewOption("  ✕  Abort  (return to main menu)", "abort"),
						).
						Value(&choice),
				),
			).WithTheme(boltTheme()).Run()

			if errors.Is(formErr, huh.ErrUserAborted) || choice == "abort" {
				return fmt.Errorf("infrastructure provisioning failed: %w", provErr)
			}
			if formErr != nil {
				return formErr
			}
			// choice == "retry" — loop
		}

		// Merge provisioned outputs into credentials and deployment.
		if outputs.DatabaseURL != "" {
			creds.DatabaseURL = outputs.DatabaseURL
		}
		if outputs.S3Bucket != "" {
			creds.S3Bucket = outputs.S3Bucket
			d.StorageConfig = &state.StorageConfig{S3Bucket: outputs.S3Bucket, S3Region: outputs.S3Region}
		}
		if outputs.S3Region != "" {
			creds.S3Region = outputs.S3Region
		}
		if outputs.S3AccessKeyID != "" {
			creds.S3AccessKeyID = outputs.S3AccessKeyID
		}
		if outputs.S3SecretKey != "" {
			creds.S3SecretAccessKey = outputs.S3SecretKey
		}
		if outputs.KubeconfigPath != "" {
			d.Kubeconfig = outputs.KubeconfigPath
			resolvedKubeconfig = outputs.KubeconfigPath
			d.Kubeconfig = resolvedKubeconfig
		}

		// For local kubeadm clusters, persist SSH connection details so destroy can reach the host.
		if infraResult.Cloud == infra.CloudLocal && infraResult.LocalCreds != nil &&
			infraResult.LocalCreds.SubType == infra.LocalKubeadm {
			d.SSHHost = infraResult.LocalCreds.SSHHost
			d.SSHUser = infraResult.LocalCreds.SSHUser
			d.SSHKeyPath = infraResult.LocalCreds.SSHKeyPath
		}

		// For EKS clusters, propagate the AWS credentials bolt used for provisioning
		// into the kubectl subprocess environment so aws-eks-get-token can authenticate.
		if infraResult.Cloud == infra.CloudAWS && infraResult.AWSCreds != nil {
			awsCreds := infraResult.AWSCreds
			if awsCreds.AccessKeyID != "" {
				d.ExtraEnv = append(d.ExtraEnv,
					"AWS_ACCESS_KEY_ID="+awsCreds.AccessKeyID,
					"AWS_SECRET_ACCESS_KEY="+awsCreds.SecretAccessKey,
				)
				if awsCreds.SessionToken != "" {
					d.ExtraEnv = append(d.ExtraEnv, "AWS_SESSION_TOKEN="+awsCreds.SessionToken)
				}
				if awsCreds.Region != "" {
					d.ExtraEnv = append(d.ExtraEnv, "AWS_DEFAULT_REGION="+awsCreds.Region)
				}
			}
		}

		showProvisionedCredentials(name, outputs)
	}

	// Print final deployment summary
	fmt.Println()
	fmt.Println(sectionStyle.Render("  Deployment summary"))
	fmt.Printf("  %s %s\n", labelStyle.Render("Name:        "), name)
	fmt.Printf("  %s %s (%s)\n", labelStyle.Render("Cluster:     "), clusterType, mode)
	fmt.Printf("  %s %s\n", labelStyle.Render("Hostname:    "), hostname)
	fmt.Printf("  %s %s\n", labelStyle.Render("Namespace:   "), namespace)
	if infraResult.Mode != infra.ProvisionBYO {
		fmt.Printf("  %s %s  (%s)\n", labelStyle.Render("Infra:       "),
			string(infraResult.Cloud), string(infraResult.Mode))
	}
	fmt.Println()

	p, err := tfe.NewProvisioner(d)
	if err != nil {
		return err
	}
	return deployWithRetry(p, d, creds)
}

// deployWithRetry runs p.Deploy and, on failure, shows a Retry / Abort picker
// so the user can fix a transient problem (expired token, missing file, etc.)
// and continue without losing the whole wizard flow. The provisioner operations
// are idempotent — already-created resources are detected and skipped on retry.
func deployWithRetry(p tfe.Provisioner, d *state.TFEDeployment, creds *credentials.TFECredentials) error {
	for {
		deployErr := p.Deploy(creds)
		if deployErr == nil {
			return nil
		}

		fmt.Println()
		fmt.Println(errorBoxStyle.Render("  ✗  " + deployErr.Error()))
		fmt.Println()

		suggestion := deployHint(deployErr)
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(amberColor).Render("  Suggestion:"))
		fmt.Println(hintStyle.Render("  " + suggestion))
		fmt.Println()

		var choice string
		formErr := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("What would you like to do?").
					Description("Already-completed steps are skipped on retry.").
					Options(
						huh.NewOption("  ↺  Retry  (after fixing the issue above)", "retry"),
						huh.NewOption("  ✕  Abort  (return to main menu)", "abort"),
					).
					Value(&choice),
			),
		).WithTheme(boltTheme()).Run()

		if errors.Is(formErr, huh.ErrUserAborted) || choice == "abort" {
			return deployErr
		}
		if formErr != nil {
			return formErr
		}
		// choice == "retry" — loop
	}
}

// deployHint returns a human-readable suggestion based on the failing step.
func deployHint(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "create TLS secret") || strings.Contains(msg, "tls-tls") ||
		strings.Contains(msg, "tfe.crt") || strings.Contains(msg, "tfe.key"):
		return "The TLS certificate or key file could not be read. Ensure the paths exist:\n" +
			"    bolt generates them automatically when 'Generate self-signed TLS' is selected.\n" +
			"    Or provide your own cert/key files and retry."

	case strings.Contains(msg, "create namespace"):
		return "Namespace creation failed. If it already exists in the cluster that is fine — retry\n" +
			"    and bolt will skip it. Otherwise check cluster connectivity:\n" +
			"    kubectl get namespaces --kubeconfig ~/.bolt/kubeconfigs/<name>.yaml"

	case strings.Contains(msg, "Unauthorized") || strings.Contains(msg, "unauthorized"):
		return "Your cloud credentials have expired. Refresh them and retry:\n" +
			"    AWS/EKS — re-authenticate via Doormat or export fresh AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY\n" +
			"    Azure   — run: az login\n" +
			"    GCP     — run: gcloud auth application-default login"

	case strings.Contains(msg, "kubeconfig") || strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "connection refused") || strings.Contains(msg, "unable to connect"):
		return "Cannot reach the cluster API server. Check:\n" +
			"    1. Your VPN / network is connected.\n" +
			"    2. The kubeconfig at ~/.bolt/kubeconfigs/<name>.yaml is valid.\n" +
			"    3. kubectl get nodes --kubeconfig ~/.bolt/kubeconfigs/<name>.yaml"

	case strings.Contains(msg, "helm install") || strings.Contains(msg, "helm upgrade"):
		return "Helm install failed — TFE pods may be in a bad state. Check:\n" +
			"    kubectl get pods -n tfe\n" +
			"    kubectl describe pod -n tfe <pod-name>\n" +
			"    kubectl logs -n tfe -l app.kubernetes.io/name=terraform-enterprise"

	case strings.Contains(msg, "ImagePull") || strings.Contains(msg, "ErrImagePull") ||
		strings.Contains(msg, "image"):
		return "Cannot pull the TFE container image. Verify:\n" +
			"    1. The image tag is correct (check https://releases.hashicorp.com/terraform-enterprise).\n" +
			"    2. Your cluster nodes can reach images.releases.hashicorp.com."

	case strings.Contains(msg, "Forbidden") || strings.Contains(msg, "forbidden"):
		return "Permission denied. Ensure the credentials you provided have the required RBAC\n" +
			"    permissions to create namespaces, secrets, and run Helm in this cluster."

	case strings.Contains(msg, "timeout") || strings.Contains(msg, "timed out") ||
		strings.Contains(msg, "deadline"):
		return "The operation timed out. The cluster may be under load or still initialising.\n" +
			"    Wait a minute, then retry — bolt will skip already-completed steps."

	case strings.Contains(msg, "create secret") || strings.Contains(msg, "tfe-secrets") ||
		strings.Contains(msg, "tfe-storage"):
		return "Kubernetes secret creation failed. Check that the namespace exists and your\n" +
			"    credentials have 'kubectl create secret' permission:\n" +
			"    kubectl auth can-i create secrets -n tfe"

	default:
		return "Review the error and kubectl output above, fix the underlying issue in your\n" +
			"    terminal or cloud console, then hit Retry — bolt will resume from where it stopped."
	}
}

// infraHint returns a human-readable suggestion based on why infra provisioning failed.
func infraHint(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "Docker is not running") || strings.Contains(msg, "docker daemon") ||
		strings.Contains(msg, "Cannot connect to the Docker daemon"):
		return "Start Docker Desktop, wait for it to finish starting, then retry."

	case strings.Contains(msg, "kind: command not found") || strings.Contains(msg, "kind not found") ||
		strings.Contains(msg, "kind version"):
		return "Install kind first: brew install kind  (Mac) or https://kind.sigs.k8s.io/docs/user/quick-start/#installation"

	case strings.Contains(msg, "ssh:") || strings.Contains(msg, "SSH") ||
		strings.Contains(msg, "dial tcp") || strings.Contains(msg, "connection refused"):
		return "SSH connection failed. Check the host is reachable, the SSH key is correct,\n" +
			"    and that your user has sudo access on the target machine."

	case strings.Contains(msg, "kubeadm") || strings.Contains(msg, "kubeadm: command not found"):
		return "kubeadm is not installed on the target machine.\n" +
			"    Install it: https://kubernetes.io/docs/setup/production-environment/tools/kubeadm/install-kubeadm/"

	case strings.Contains(msg, "Unauthorized") || strings.Contains(msg, "ExpiredToken") ||
		strings.Contains(msg, "credential"):
		return "Cloud credentials have expired. Refresh them and retry:\n" +
			"    AWS — re-authenticate via Doormat or export fresh AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY\n" +
			"    Azure — run: az login\n" +
			"    GCP   — run: gcloud auth application-default login"

	case strings.Contains(msg, "quota") || strings.Contains(msg, "limit exceeded") ||
		strings.Contains(msg, "LimitExceeded"):
		return "Cloud resource quota exceeded. Request a quota increase in your cloud console, or\n" +
			"    destroy existing unused resources to free capacity, then retry."

	case strings.Contains(msg, "VPC") || strings.Contains(msg, "subnet"):
		return "VPC or subnet provisioning failed. Check that the region has available CIDR space\n" +
			"    and that your account can create VPCs."

	default:
		return "Review the error above, fix the underlying issue, then retry — bolt will skip\n" +
			"    already-completed provisioning steps."
	}
}

func normalizeHostname(h string) string {
	h = strings.TrimSpace(h)
	h = strings.TrimPrefix(h, "https://")
	h = strings.TrimPrefix(h, "http://")
	return strings.TrimRight(h, "/")
}

// buildInfraConfig converts an InfraWizardResult into the InfraConfig the
// orchestrator accepts.
func buildInfraConfig(r *InfraWizardResult, deploymentName string) *infra.InfraConfig {
	prefix := sanitizePrefix(deploymentName)
	cfg := &infra.InfraConfig{
		Mode:       r.Mode,
		Cloud:      r.Cloud,
		Database:   r.Database,
		Sizing:     r.Sizing,
		NamePrefix: prefix,
		Tags: map[string]string{
			"bolt:deployment": deploymentName,
			"bolt:managed":    "true",
		},
	}

	if r.AWSCreds != nil {
		cfg.AWS = &infra.AWSCreds{
			AssumeRoleARN:   r.AWSCreds.AssumeRoleARN,
			Region:          r.AWSCreds.Region,
			AccessKeyID:     r.AWSCreds.AccessKeyID,
			SecretAccessKey: r.AWSCreds.SecretAccessKey,
			SessionToken:    r.AWSCreds.SessionToken,
		}
	}
	if r.AzureCreds != nil {
		cfg.Azure = &infra.AzureCreds{
			SubscriptionID: r.AzureCreds.SubscriptionID,
			TenantID:       r.AzureCreds.TenantID,
			ClientID:       r.AzureCreds.ClientID,
			ClientSecret:   r.AzureCreds.ClientSecret,
			ResourceGroup:  r.AzureCreds.ResourceGroup,
			Location:       r.AzureCreds.Location,
		}
	}
	if r.GCPCreds != nil {
		cfg.GCP = &infra.GCPCreds{
			ProjectID:       r.GCPCreds.ProjectID,
			Region:          r.GCPCreds.Region,
			Zone:            r.GCPCreds.Zone,
			ServiceAcctJSON: r.GCPCreds.ServiceAcctJSON,
		}
	}
	if r.LocalCreds != nil {
		cfg.Local = r.LocalCreds
	}
	return cfg
}


// ── Docker deploy wizard ───────────────────────────────────────────────────────
//
// Groups:
//   1. Basic info (name, hostname, mode, generateTLS)
//   2. Remote host (optional SSH host)
//   3. SSH user (hidden when no SSH host)
//   4. Core credentials
//   5. TLS cert+key (hidden when generate-TLS)
//   6. External storage (hidden for disk mode)
//   7. Redis URL (hidden unless active-active)

func interactiveDeployDocker() error {
	var (
		name        string
		hostname    string
		mode        = "disk"
		generateTLS bool
		sshHost     string
		sshUser     string

		license            string
		encryptionPassword string
		tlsCertPath        string
		tlsKeyPath         string

		databaseURL       string
		s3Bucket          string
		s3Region          string
		s3AccessKeyID     string
		s3SecretAccessKey string
		redisURL          string
	)

	err := huh.NewForm(
		// ── 1. Basic info ─────────────────────────────────────────────────────
		huh.NewGroup(
			huh.NewInput().
				Title("Deployment name").
				Placeholder("local-tfe").
				Value(&name).
				Validate(notEmpty("deployment name")),
			huh.NewInput().
				Title("Hostname (FQDN)").
				Placeholder("tfe.example.com").
				Value(&hostname).
				Validate(notEmpty("hostname")),
			huh.NewSelect[string]().
				Title("Operational mode").
				Options(
					huh.NewOption("Disk  — embedded storage, single node", "disk"),
					huh.NewOption("External  — PostgreSQL + S3", "external"),
					huh.NewOption("Active-Active  — PostgreSQL + S3 + Redis", "active-active"),
				).
				Value(&mode),
			huh.NewConfirm().
				Title("Generate self-signed TLS certificate?").
				Description("For dev/test only — not for production").
				Value(&generateTLS),
		),

		// ── 2. Remote Docker host (optional) ──────────────────────────────────
		huh.NewGroup(
			huh.NewInput().
				Title("Remote Docker host  (leave blank for local Docker)").
				Placeholder("10.0.0.5  or  docker.example.com").
				Value(&sshHost),
		),

		// ── 3. SSH user (only when remote host is set) ────────────────────────
		huh.NewGroup(
			huh.NewInput().
				Title("SSH user for remote host").
				Placeholder("ubuntu").
				Value(&sshUser),
		).WithHideFunc(func() bool { return strings.TrimSpace(sshHost) == "" }),

		// ── 4. Core credentials ───────────────────────────────────────────────
		huh.NewGroup(
			huh.NewInput().
				Title("TFE License").
				Description("Your Terraform Enterprise license key").
				EchoMode(huh.EchoModePassword).
				Value(&license).
				Validate(notEmpty("license")),
			huh.NewInput().
				Title("Encryption password").
				EchoMode(huh.EchoModePassword).
				Value(&encryptionPassword).
				Validate(notEmpty("encryption password")),
		),

		// ── 5. TLS paths (skipped when self-signed TLS is chosen) ─────────────
		huh.NewGroup(
			huh.NewInput().
				Title("TLS certificate path").
				Placeholder("/path/to/tfe.crt").
				Value(&tlsCertPath).
				Validate(notEmpty("TLS certificate path")),
			huh.NewInput().
				Title("TLS key path").
				Placeholder("/path/to/tfe.key").
				Value(&tlsKeyPath).
				Validate(notEmpty("TLS key path")),
		).WithHideFunc(func() bool { return generateTLS }),

		// ── 6. External storage (skipped for disk mode) ───────────────────────
		huh.NewGroup(
			huh.NewInput().
				Title("PostgreSQL database URL").
				Placeholder("postgres://user:pass@host:5432/tfe").
				EchoMode(huh.EchoModePassword).
				Value(&databaseURL).
				Validate(notEmpty("database URL")),
			huh.NewInput().
				Title("S3 bucket name").
				Value(&s3Bucket).
				Validate(notEmpty("S3 bucket")),
			huh.NewInput().
				Title("S3 region").
				Placeholder("us-east-1").
				Value(&s3Region).
				Validate(notEmpty("S3 region")),
			huh.NewInput().
				Title("S3 access key ID").
				Value(&s3AccessKeyID).
				Validate(notEmpty("S3 access key ID")),
			huh.NewInput().
				Title("S3 secret access key").
				EchoMode(huh.EchoModePassword).
				Value(&s3SecretAccessKey).
				Validate(notEmpty("S3 secret access key")),
		).WithHideFunc(func() bool { return mode == "disk" || mode == "" }),

		// ── 7. Redis (active-active only) ─────────────────────────────────────
		huh.NewGroup(
			huh.NewInput().
				Title("Redis URL").
				Placeholder("redis://host:6379").
				Value(&redisURL).
				Validate(notEmpty("Redis URL")),
		).WithHideFunc(func() bool { return mode != "active-active" }),
	).WithTheme(boltTheme()).Run()

	if errors.Is(err, huh.ErrUserAborted) {
		fmt.Println("Cancelled.")
		return nil
	}
	if err != nil {
		return err
	}

	// Print summary
	fmt.Println()
	fmt.Println(sectionStyle.Render("  Deployment summary"))
	fmt.Printf("  %s %s\n", labelStyle.Render("Name:        "), name)
	fmt.Printf("  %s Docker (%s)\n", labelStyle.Render("Backend:     "), mode)
	fmt.Printf("  %s %s\n", labelStyle.Render("Hostname:    "), hostname)
	if strings.TrimSpace(sshHost) != "" {
		fmt.Printf("  %s %s\n", labelStyle.Render("Remote host: "), sshHost)
	}
	fmt.Printf("  %s %v\n", labelStyle.Render("Self-signed: "), generateTLS)
	fmt.Println()

	var confirmed bool
	err = huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Proceed with deployment?").
				Affirmative("Deploy").
				Negative("Cancel").
				Value(&confirmed),
		),
	).WithTheme(boltTheme()).Run()

	if errors.Is(err, huh.ErrUserAborted) || !confirmed {
		fmt.Println("Deployment cancelled.")
		return nil
	}
	if err != nil {
		return err
	}

	home, _ := os.UserHomeDir()

	if generateTLS {
		tlsDir := filepath.Join(home, ".bolt", "tls", name)
		tlsCertPath = filepath.Join(tlsDir, "tfe.crt")
		tlsKeyPath = filepath.Join(tlsDir, "tfe.key")
	}

	dataDir := filepath.Join(home, ".bolt", "data", name)

	d := &state.TFEDeployment{
		Name:          name,
		Backend:       state.BackendDocker,
		Mode:          state.OperationalMode(mode),
		Hostname:      hostname,
		ImageTag:      "latest",
		TLSCertPath:   tlsCertPath,
		TLSKeyPath:    tlsKeyPath,
		SelfSignedTLS: generateTLS,
		DataDir:       dataDir,
		SSHHost:       strings.TrimSpace(sshHost),
		SSHUser:       sshUser,
		Status:        state.StatusPending,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if s3Bucket != "" {
		d.StorageConfig = &state.StorageConfig{S3Bucket: s3Bucket, S3Region: s3Region}
	}

	creds := &credentials.TFECredentials{
		License:           license,
		EncryptionPassword: encryptionPassword,
		TLSCert:           tlsCertPath,
		TLSKey:            tlsKeyPath,
		DatabaseURL:       databaseURL,
		S3Bucket:          s3Bucket,
		S3Region:          s3Region,
		S3AccessKeyID:     s3AccessKeyID,
		S3SecretAccessKey: s3SecretAccessKey,
		RedisURL:          redisURL,
	}

	p, err := tfe.NewProvisioner(d)
	if err != nil {
		return err
	}
	return p.Deploy(creds)
}

// notEmpty returns a huh validation function that rejects blank inputs.
func notEmpty(field string) func(string) error {
	return func(v string) error {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("%s is required", field)
		}
		return nil
	}
}
