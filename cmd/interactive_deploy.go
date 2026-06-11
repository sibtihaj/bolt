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
	"github.com/sibtihaj/bolt/app/credentials"
	"github.com/sibtihaj/bolt/app/infra"
	"github.com/sibtihaj/bolt/app/state"
	"github.com/sibtihaj/bolt/app/tfe"
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
		hostname    string
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

	// Pre-select cluster type from infra wizard cloud choice.
	switch infraResult.Cloud {
	case infra.CloudAWS:
		clusterType = "eks"
	case infra.CloudAzure:
		clusterType = "aks"
	case infra.CloudGCP:
		clusterType = "gke"
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

	if namespace == "" {
		namespace = "tfe"
	}

	home, _ := os.UserHomeDir()

	if generateTLS {
		tlsDir := filepath.Join(home, ".bolt", "tls", name)
		tlsCertPath = filepath.Join(tlsDir, "tfe.crt")
		tlsKeyPath = filepath.Join(tlsDir, "tfe.key")
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

		outputs, err := infra.Provision(context.Background(), infraCfg, infraStateRec)
		if err != nil {
			outputs, err = handleAWSProvisionError(context.Background(), err, infraCfg, infraStateRec)
			if err != nil {
				return fmt.Errorf("infrastructure provisioning failed: %w", err)
			}
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
	return p.Deploy(creds)
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
