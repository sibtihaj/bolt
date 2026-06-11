package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/sibtihaj/bolt/app/infra"
	"github.com/sibtihaj/bolt/app/preflight"
)

// InfraWizardResult holds everything the infra wizard collected.
type InfraWizardResult struct {
	Mode         infra.ProvisionMode
	Cloud        infra.CloudProvider
	Database     infra.DatabaseChoice
	Sizing       infra.ResourceSizing
	AWSCreds     *preflight.AWSConfig
	AzureCreds   *preflight.AzureConfig
	GCPCreds     *preflight.GCPConfig
	// Set after validation
	AWSIdentity  *preflight.AWSIdentity
}

// ── Access requirements displayed before credential entry ─────────────────────

var awsRequirements = []string{
	"  sts:GetCallerIdentity  (credential check)",
	"  sts:AssumeRole         (if using role-based access)",
	"  s3:CreateBucket, s3:PutObject, s3:GetObject, s3:DeleteObject",
	"  rds:CreateDBInstance, rds:DescribeDBInstances  (managed DB)",
	"  eks:CreateCluster, eks:DescribeCluster, eks:CreateNodegroup  (full provision)",
	"  ec2:CreateVpc, ec2:CreateSubnet, ec2:CreateInternetGateway  (full provision)",
	"  iam:CreateRole, iam:AttachRolePolicy  (EKS roles)",
}

var azureRequirements = []string{
	"  Microsoft.Resources/resourceGroups/write",
	"  Microsoft.Storage/storageAccounts/write  +  listKeys/action",
	"  Microsoft.DBforPostgreSQL/flexibleServers/write  (managed DB)",
	"  Microsoft.ContainerService/managedClusters/write  (full provision)",
	"  Microsoft.Network/virtualNetworks/write  (full provision)",
}

var gcpRequirements = []string{
	"  resourcemanager.projects.get",
	"  storage.buckets.create  +  storage.objects.create/get/delete",
	"  cloudsql.instances.create  (managed DB)",
	"  container.clusters.create  +  container.operations.get  (full provision)",
	"  compute.networks.create  (full provision)",
}

// interactiveInfraWarning prints a read-only panel that lists the cloud
// permissions bolt needs, then asks the user to confirm they understand.
func interactiveInfraWarning(cloud string) error {
	var reqs []string
	var cloudLabel string
	switch cloud {
	case "aws":
		reqs = awsRequirements
		cloudLabel = "AWS"
	case "azure":
		reqs = azureRequirements
		cloudLabel = "Azure"
	case "gcp":
		reqs = gcpRequirements
		cloudLabel = "GCP"
	}

	warnStyle := lipgloss.NewStyle().Bold(true).Foreground(amberColor)
	listStyle := lipgloss.NewStyle().Foreground(cyanBright)
	fmt.Println()
	fmt.Println(warnStyle.Render("  ⚡ bolt needs the following " + cloudLabel + " permissions:"))
	fmt.Println()
	for _, r := range reqs {
		fmt.Println(listStyle.Render(r))
	}
	fmt.Println()
	fmt.Println(hintStyle.Render("  Tip: assign these permissions to the IAM role / service principal bolt will use."))
	fmt.Println()

	var ok bool
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("I have granted the required permissions").
				Affirmative("Continue").
				Negative("Go back").
				Value(&ok),
		),
	).WithTheme(boltTheme()).Run()

	if errors.Is(err, huh.ErrUserAborted) || !ok {
		return fmt.Errorf("cancelled")
	}
	return err
}

// interactiveInfraSource asks how the user wants infrastructure managed.
// Returns (ProvisionMode, CloudProvider).
func interactiveInfraSource(backend string) (infra.ProvisionMode, infra.CloudProvider, error) {
	var modeStr, cloudStr string

	groups := []*huh.Group{
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("How would you like to provision infrastructure?").
				Description("bolt can create everything from scratch, or you can bring your own.").
				Options(
					huh.NewOption("  ★  Provision everything  (VPC + cluster + DB + storage)", "all"),
					huh.NewOption("  ◎  Use existing cluster  (bolt creates DB + storage only)", "storage-only"),
					huh.NewOption("  →  Bring your own  (connect to existing cluster + storage)", "byo"),
				).
				Value(&modeStr),
		),
	}

	// Only ask for cloud if provisioning something
	groups = append(groups,
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Which cloud provider?").
				Options(
					huh.NewOption("  ☁  Amazon Web Services  (EKS / RDS / S3)", "aws"),
					huh.NewOption("  ☁  Microsoft Azure  (AKS / Azure DB / Blob)", "azure"),
					huh.NewOption("  ☁  Google Cloud  (GKE / Cloud SQL / GCS)", "gcp"),
				).
				Value(&cloudStr),
		).WithHideFunc(func() bool { return modeStr == "byo" }),
	)

	err := huh.NewForm(groups...).WithTheme(boltTheme()).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return "", "", fmt.Errorf("cancelled")
	}
	if err != nil {
		return "", "", err
	}

	if modeStr == "byo" {
		cloudStr = ""
	}
	return infra.ProvisionMode(modeStr), infra.CloudProvider(cloudStr), nil
}

// interactiveAWSCredentials collects AWS credentials and validates them via STS.
// If Doormat is available it is offered as the first (recommended) option.
func interactiveAWSCredentials() (*preflight.AWSConfig, *preflight.AWSIdentity, error) {
	// If doormat is installed, offer it upfront.
	if preflight.DoormatAvailable() {
		var source string
		err := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("How would you like to provide AWS credentials?").
					Options(
						huh.NewOption("  ⚡  Use Doormat  (recommended)", "doormat"),
						huh.NewOption("  ✎  Enter credentials manually", "manual"),
					).
					Value(&source),
			),
		).WithTheme(boltTheme()).Run()
		if errors.Is(err, huh.ErrUserAborted) {
			return nil, nil, fmt.Errorf("cancelled")
		}
		if err != nil {
			return nil, nil, err
		}
		if source == "doormat" {
			return interactiveAWSViaDoormat()
		}
	}

	var authMode string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("How does bolt authenticate to AWS?").
				Description("Role assumption is the recommended enterprise pattern.").
				Options(
					huh.NewOption("  ↗  Assume an IAM role in your account  (recommended)", "role"),
					huh.NewOption("  🔑  Static access key + secret", "static"),
					huh.NewOption("  ⚙  Use ambient credentials  (env / ~/.aws/credentials)", "ambient"),
				).
				Value(&authMode),
		),
	).WithTheme(boltTheme()).Run()

	if errors.Is(err, huh.ErrUserAborted) {
		return nil, nil, fmt.Errorf("cancelled")
	}
	if err != nil {
		return nil, nil, err
	}

	cfg := &preflight.AWSConfig{}

	switch authMode {
	case "role":
		err = huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("IAM Role ARN").
					Placeholder("arn:aws:iam::123456789012:role/BoltDeployRole").
					Value(&cfg.AssumeRoleARN).
					Validate(notEmpty("IAM role ARN")),
				huh.NewInput().
					Title("AWS Region").
					Placeholder("us-east-1").
					Value(&cfg.Region).
					Validate(notEmpty("AWS region")),
			),
		).WithTheme(boltTheme()).Run()

	case "static":
		err = huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Access Key ID").
					Placeholder("AKIAIOSFODNN7EXAMPLE").
					Value(&cfg.AccessKeyID).
					Validate(notEmpty("access key ID")),
				huh.NewInput().
					Title("Secret Access Key").
					EchoMode(huh.EchoModePassword).
					Value(&cfg.SecretAccessKey).
					Validate(notEmpty("secret access key")),
				huh.NewInput().
					Title("AWS Region").
					Placeholder("us-east-1").
					Value(&cfg.Region).
					Validate(notEmpty("AWS region")),
			),
		).WithTheme(boltTheme()).Run()

	case "ambient":
		err = huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("AWS Region").
					Placeholder("us-east-1").
					Value(&cfg.Region).
					Validate(notEmpty("AWS region")),
			),
		).WithTheme(boltTheme()).Run()
	}

	if errors.Is(err, huh.ErrUserAborted) {
		return nil, nil, fmt.Errorf("cancelled")
	}
	if err != nil {
		return nil, nil, err
	}

	// Validate credentials.
	fmt.Print("\n" + hintStyle.Render("  Validating AWS credentials…  "))
	identity, err := preflight.ValidateAWSCredentials(cfg)
	if err != nil {
		fmt.Println(lipgloss.NewStyle().Foreground(redColor).Render("✗"))
		return nil, nil, fmt.Errorf("AWS credential validation: %w", err)
	}
	fmt.Println(lipgloss.NewStyle().Foreground(greenColor).Render("✓"))
	fmt.Printf("  %s %s\n", labelStyle.Render("Account:"), identity.AccountID)
	fmt.Printf("  %s %s\n\n", labelStyle.Render("ARN:"), identity.ARN)

	return cfg, identity, nil
}

// interactiveAWSViaDoormat handles the Doormat-based AWS credential flow:
// login (if needed) → role picker → region → short-lived credentials.
func interactiveAWSViaDoormat() (*preflight.AWSConfig, *preflight.AWSIdentity, error) {
	// Check if the doormat session is still valid; if not, run login.
	if !preflight.DoormatSessionValid() {
		fmt.Println()
		fmt.Println(lipgloss.NewStyle().Foreground(tfeColor).Bold(true).Render("  ⚡  Starting Doormat login — your browser will open to authenticate."))
		fmt.Println()
		if err := preflight.DoormatLogin(); err != nil {
			return nil, nil, err
		}
		fmt.Println()
	}

	// Fetch available roles.
	fmt.Print(hintStyle.Render("  Fetching eligible roles from Doormat…  "))
	roles, err := preflight.DoormatListRoles()
	if err != nil {
		fmt.Println(lipgloss.NewStyle().Foreground(redColor).Render("✗"))
		return nil, nil, err
	}
	fmt.Println(lipgloss.NewStyle().Foreground(greenColor).Render("✓"))

	// Build select options from role ARNs.
	opts := make([]huh.Option[string], len(roles))
	for i, arn := range roles {
		opts[i] = huh.NewOption(preflight.DoormatRoleLabel(arn), arn)
	}

	var selectedRole, region string
	err = huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Which role would you like to use?").
				Options(opts...).
				Value(&selectedRole),
		),
		huh.NewGroup(
			huh.NewInput().
				Title("AWS Region").
				Placeholder("us-east-1").
				Value(&region).
				Validate(notEmpty("AWS region")),
		),
	).WithTheme(boltTheme()).Run()

	if errors.Is(err, huh.ErrUserAborted) {
		return nil, nil, fmt.Errorf("cancelled")
	}
	if err != nil {
		return nil, nil, err
	}

	// Retrieve credentials from Doormat.
	fmt.Print("\n" + hintStyle.Render("  Retrieving credentials from Doormat…  "))
	cfg, creds, err := preflight.DoormatGetCredentials(selectedRole, region)
	if err != nil {
		fmt.Println(lipgloss.NewStyle().Foreground(redColor).Render("✗"))
		return nil, nil, err
	}
	fmt.Println(lipgloss.NewStyle().Foreground(greenColor).Render("✓"))

	// Validate via STS to confirm identity.
	fmt.Print(hintStyle.Render("  Validating credentials via STS…  "))
	identity, err := preflight.ValidateAWSCredentials(cfg)
	if err != nil {
		fmt.Println(lipgloss.NewStyle().Foreground(redColor).Render("✗"))
		return nil, nil, fmt.Errorf("AWS credential validation: %w", err)
	}
	fmt.Println(lipgloss.NewStyle().Foreground(greenColor).Render("✓"))
	fmt.Printf("  %s %s\n", labelStyle.Render("Account:"), identity.AccountID)
	fmt.Printf("  %s %s\n", labelStyle.Render("ARN:    "), identity.ARN)
	fmt.Printf("  %s %s\n\n", labelStyle.Render("Expiry: "),
		lipgloss.NewStyle().Foreground(cyanBright).Render(preflight.DoormatExpiresIn(creds.Expiration)))

	return cfg, identity, nil
}

// interactiveAzureCredentials collects Azure service-principal credentials and validates them.
func interactiveAzureCredentials() (*preflight.AzureConfig, error) {
	cfg := &preflight.AzureConfig{}

	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Azure Tenant ID").
				Placeholder("xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx").
				Value(&cfg.TenantID).
				Validate(notEmpty("tenant ID")),
			huh.NewInput().
				Title("Client ID  (service principal)").
				Value(&cfg.ClientID).
				Validate(notEmpty("client ID")),
			huh.NewInput().
				Title("Client Secret").
				EchoMode(huh.EchoModePassword).
				Value(&cfg.ClientSecret).
				Validate(notEmpty("client secret")),
			huh.NewInput().
				Title("Subscription ID").
				Value(&cfg.SubscriptionID).
				Validate(notEmpty("subscription ID")),
		),
		huh.NewGroup(
			huh.NewInput().
				Title("Resource group  (existing or bolt will create it)").
				Placeholder("rg-tfe-prod").
				Value(&cfg.ResourceGroup).
				Validate(notEmpty("resource group")),
			huh.NewInput().
				Title("Azure location").
				Placeholder("eastus").
				Value(&cfg.Location).
				Validate(notEmpty("Azure location")),
		),
	).WithTheme(boltTheme()).Run()

	if errors.Is(err, huh.ErrUserAborted) {
		return nil, fmt.Errorf("cancelled")
	}
	if err != nil {
		return nil, err
	}

	fmt.Print("\n" + hintStyle.Render("  Validating Azure credentials…  "))
	if err := preflight.ValidateAzureCredentials(cfg); err != nil {
		fmt.Println(lipgloss.NewStyle().Foreground(redColor).Render("✗"))
		return nil, fmt.Errorf("Azure credential validation: %w", err)
	}
	fmt.Println(lipgloss.NewStyle().Foreground(greenColor).Render("✓"))
	fmt.Printf("  %s %s\n\n", labelStyle.Render("Subscription:"), cfg.SubscriptionID)

	return cfg, nil
}

// interactiveGCPCredentials collects GCP service-account credentials and validates them.
func interactiveGCPCredentials() (*preflight.GCPConfig, error) {
	cfg := &preflight.GCPConfig{}

	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("GCP Project ID").
				Placeholder("my-gcp-project").
				Value(&cfg.ProjectID).
				Validate(notEmpty("project ID")),
			huh.NewInput().
				Title("Service Account key file path").
				Placeholder("/path/to/sa-key.json").
				Value(&cfg.ServiceAcctJSON).
				Validate(notEmpty("service account key file")),
			huh.NewInput().
				Title("Region").
				Placeholder("us-central1").
				Value(&cfg.Region).
				Validate(notEmpty("region")),
			huh.NewInput().
				Title("Zone  (for GKE node pools)").
				Placeholder("us-central1-a").
				Value(&cfg.Zone).
				Validate(notEmpty("zone")),
		),
	).WithTheme(boltTheme()).Run()

	if errors.Is(err, huh.ErrUserAborted) {
		return nil, fmt.Errorf("cancelled")
	}
	if err != nil {
		return nil, err
	}

	fmt.Print("\n" + hintStyle.Render("  Validating GCP credentials…  "))
	if err := preflight.ValidateGCPCredentials(cfg); err != nil {
		fmt.Println(lipgloss.NewStyle().Foreground(redColor).Render("✗"))
		return nil, fmt.Errorf("GCP credential validation: %w", err)
	}
	fmt.Println(lipgloss.NewStyle().Foreground(greenColor).Render("✓"))
	fmt.Printf("  %s %s\n\n", labelStyle.Render("Project:"), cfg.ProjectID)

	return cfg, nil
}

// interactiveSizing asks the user to choose a resource sizing tier.
func interactiveSizing(cloud infra.CloudProvider) (infra.ResourceSizing, error) {
	min := infra.DefaultSizing(infra.TierMinimum, cloud)
	rec := infra.DefaultSizing(infra.TierRecommended, cloud)

	var tierStr string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Resource sizing").
				Description("All resources can be resized after deployment.").
				Options(
					huh.NewOption(fmt.Sprintf("  ▸  Minimum    — %s × %d nodes, %s DB",
						min.Nodes.InstanceType, min.Nodes.NodeCount, min.DBClass), "minimum"),
					huh.NewOption(fmt.Sprintf("  ★  Recommended — %s × %d nodes, %s DB  (HashiCorp reference)",
						rec.Nodes.InstanceType, rec.Nodes.NodeCount, rec.DBClass), "recommended"),
					huh.NewOption("  ⚙  Custom      — specify instance types manually", "custom"),
				).
				Value(&tierStr),
		),
	).WithTheme(boltTheme()).Run()

	if errors.Is(err, huh.ErrUserAborted) {
		return infra.ResourceSizing{}, fmt.Errorf("cancelled")
	}
	if err != nil {
		return infra.ResourceSizing{}, err
	}

	if tierStr != "custom" {
		return infra.DefaultSizing(infra.ResourceTier(tierStr), cloud), nil
	}

	// Custom sizing wizard
	sizing := infra.DefaultSizing(infra.TierRecommended, cloud)
	nodeType := sizing.Nodes.InstanceType
	nodeCount := fmt.Sprintf("%d", sizing.Nodes.NodeCount)
	dbClass := sizing.DBClass
	dbStorage := fmt.Sprintf("%d", sizing.DBStorage)

	err = huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Node instance type").
				Placeholder(sizing.Nodes.InstanceType).
				Value(&nodeType).
				Validate(notEmpty("instance type")),
			huh.NewInput().
				Title("Number of nodes").
				Placeholder(fmt.Sprintf("%d", sizing.Nodes.NodeCount)).
				Value(&nodeCount),
			huh.NewInput().
				Title("Database instance class").
				Placeholder(sizing.DBClass).
				Value(&dbClass).
				Validate(notEmpty("DB class")),
			huh.NewInput().
				Title("Database storage  (GiB)").
				Placeholder(fmt.Sprintf("%d", sizing.DBStorage)).
				Value(&dbStorage),
		),
	).WithTheme(boltTheme()).Run()

	if errors.Is(err, huh.ErrUserAborted) {
		return infra.ResourceSizing{}, fmt.Errorf("cancelled")
	}
	if err != nil {
		return infra.ResourceSizing{}, err
	}

	var count, storage int
	fmt.Sscanf(nodeCount, "%d", &count)
	fmt.Sscanf(dbStorage, "%d", &storage)
	if count <= 0 {
		count = sizing.Nodes.NodeCount
	}
	if storage <= 0 {
		storage = sizing.DBStorage
	}

	return infra.ResourceSizing{
		Tier:      infra.TierCustom,
		Nodes:     infra.NodeSizing{InstanceType: nodeType, NodeCount: count},
		DBClass:   dbClass,
		DBStorage: storage,
	}, nil
}

// interactiveDatabaseChoice asks where PostgreSQL should run.
func interactiveDatabaseChoice(mode infra.ProvisionMode) (infra.DatabaseChoice, error) {
	if mode == infra.ProvisionBYO {
		return infra.DBBYO, nil
	}

	var dbChoice string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Where should PostgreSQL run?").
				Options(
					huh.NewOption("  ☁  Managed service  (RDS / Cloud SQL / Azure DB)  — recommended", "managed"),
					huh.NewOption("  ◎  In-cluster pod   (StatefulSet inside K8s)  — lower cost", "in-cluster"),
					huh.NewOption("  →  Bring your own   (supply a connection string)", "byo"),
				).
				Value(&dbChoice),
		),
	).WithTheme(boltTheme()).Run()

	if errors.Is(err, huh.ErrUserAborted) {
		return "", fmt.Errorf("cancelled")
	}
	if err != nil {
		return "", err
	}
	return infra.DatabaseChoice(dbChoice), nil
}

// interactiveInfraPlan prints a human-readable summary of what bolt will provision.
func interactiveInfraPlan(result *InfraWizardResult, deploymentName string) error {
	fmt.Println()
	fmt.Println(sectionStyle.Render("  Infrastructure plan"))
	fmt.Println()

	row := func(label, value string) {
		fmt.Printf("  %s %s\n", labelStyle.Render(padRight(label, 22)), tcStyle.Render(value))
	}

	row("Provision mode:", string(result.Mode))
	if result.Cloud != "" {
		row("Cloud:", string(result.Cloud))
	}
	row("Database:", string(result.Database))

	if result.Mode != infra.ProvisionBYO {
		row("Node type:", result.Sizing.Nodes.InstanceType)
		row("Node count:", fmt.Sprintf("%d", result.Sizing.Nodes.NodeCount))
		if result.Database == infra.DBManaged {
			row("DB class:", result.Sizing.DBClass)
			row("DB storage:", fmt.Sprintf("%d GiB", result.Sizing.DBStorage))
		}
	}

	if result.AWSIdentity != nil {
		row("AWS account:", result.AWSIdentity.AccountID)
		row("IAM principal:", trimARN(result.AWSIdentity.ARN))
	}

	namePrefix := sanitizePrefix(deploymentName)
	fmt.Println()

	cloudItems := buildProvisionPlanItems(result, namePrefix)
	if len(cloudItems) > 0 {
		fmt.Println(hintStyle.Render("  Resources bolt will create:"))
		for _, item := range cloudItems {
			fmt.Println(lipgloss.NewStyle().Foreground(cyanBright).Render("    + " + item))
		}
		fmt.Println()
	}

	var confirmed bool
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Proceed with this plan?").
				Affirmative("Provision & Deploy").
				Negative("Cancel").
				Value(&confirmed),
		),
	).WithTheme(boltTheme()).Run()

	if errors.Is(err, huh.ErrUserAborted) || !confirmed {
		return fmt.Errorf("cancelled")
	}
	return err
}

func buildProvisionPlanItems(result *InfraWizardResult, prefix string) []string {
	if result.Mode == infra.ProvisionBYO {
		return nil
	}
	var items []string
	switch result.Cloud {
	case infra.CloudAWS:
		if result.Mode == infra.ProvisionAll {
			items = append(items, "VPC: "+prefix+"-vpc  (10.0.0.0/16, 2 AZs)")
			items = append(items, "EKS cluster: "+prefix)
			items = append(items, "Node group: "+prefix+"-nodes  ("+result.Sizing.Nodes.InstanceType+
				" × "+fmt.Sprintf("%d", result.Sizing.Nodes.NodeCount)+")")
		}
		items = append(items, "S3 bucket: "+prefix+"-tfe")
		if result.Database == infra.DBManaged {
			items = append(items, "RDS instance: "+prefix+"-db  (PostgreSQL 15, "+result.Sizing.DBClass+")")
		} else if result.Database == infra.DBInCluster {
			items = append(items, "PostgreSQL StatefulSet in namespace tfe")
		}
	case infra.CloudAzure:
		if result.Mode == infra.ProvisionAll {
			items = append(items, "AKS cluster: "+prefix)
		}
		items = append(items, "Storage account: "+sanitizeStorageAccount(prefix)+"tfe")
		if result.Database == infra.DBManaged {
			items = append(items, "Azure DB for PostgreSQL: "+prefix+"-db")
		} else if result.Database == infra.DBInCluster {
			items = append(items, "PostgreSQL StatefulSet in namespace tfe")
		}
	case infra.CloudGCP:
		if result.Mode == infra.ProvisionAll {
			items = append(items, "GKE cluster: "+prefix)
		}
		items = append(items, "GCS bucket: "+prefix+"-tfe")
		if result.Database == infra.DBManaged {
			items = append(items, "Cloud SQL instance: "+prefix+"-db  (PostgreSQL 15)")
		} else if result.Database == infra.DBInCluster {
			items = append(items, "PostgreSQL StatefulSet in namespace tfe")
		}
	}
	return items
}

// maskPassword replaces the password portion of a postgres:// URL with ••••••••.
func maskPassword(dbURL string) string {
	// postgres://user:PASSWORD@host:port/db
	if idx := strings.Index(dbURL, "://"); idx >= 0 {
		rest := dbURL[idx+3:]
		if at := strings.LastIndex(rest, "@"); at >= 0 {
			userPass := rest[:at]
			if colon := strings.Index(userPass, ":"); colon >= 0 {
				return dbURL[:idx+3] + userPass[:colon+1] + "••••••••" + "@" + rest[at+1:]
			}
		}
	}
	return dbURL
}

func trimARN(arn string) string {
	if idx := strings.LastIndex(arn, "/"); idx >= 0 {
		return arn[idx+1:]
	}
	return arn
}

func sanitizePrefix(name string) string {
	name = strings.ToLower(name)
	result := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			result = append(result, c)
		} else if len(result) > 0 && result[len(result)-1] != '-' {
			result = append(result, '-')
		}
	}
	s := strings.Trim(string(result), "-")
	if len(s) > 20 {
		s = s[:20]
	}
	return s
}

func sanitizeStorageAccount(prefix string) string {
	result := make([]byte, 0, len(prefix))
	for i := 0; i < len(prefix); i++ {
		c := prefix[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			result = append(result, c)
		}
	}
	s := string(result)
	if len(s) > 16 {
		s = s[:16]
	}
	return s
}

// RunInfraWizard orchestrates the full infra wizard and returns an InfraWizardResult.
// Call this before the TFE-specific deploy wizard when the user picks K8s backend.
func RunInfraWizard(deploymentName string) (*InfraWizardResult, error) {
	mode, cloud, err := interactiveInfraSource("k8s")
	if err != nil {
		return nil, err
	}

	result := &InfraWizardResult{
		Mode:  mode,
		Cloud: cloud,
	}

	// BYO: skip everything — credentials/sizing/DB are handled by existing wizard.
	if mode == infra.ProvisionBYO {
		result.Database = infra.DBBYO
		return result, nil
	}

	// Show permission requirements before asking for credentials.
	if err := interactiveInfraWarning(string(cloud)); err != nil {
		return nil, err
	}

	// Collect cloud-specific credentials.
	switch cloud {
	case infra.CloudAWS:
		awsCreds, identity, err := interactiveAWSCredentials()
		if err != nil {
			return nil, err
		}
		result.AWSCreds = awsCreds
		result.AWSIdentity = identity

	case infra.CloudAzure:
		azCreds, err := interactiveAzureCredentials()
		if err != nil {
			return nil, err
		}
		result.AzureCreds = azCreds

	case infra.CloudGCP:
		gcpCreds, err := interactiveGCPCredentials()
		if err != nil {
			return nil, err
		}
		result.GCPCreds = gcpCreds
	}

	// Resource sizing (only when provisioning cluster or storage).
	if mode == infra.ProvisionAll || mode == infra.ProvisionStorageOnly {
		sizing, err := interactiveSizing(cloud)
		if err != nil {
			return nil, err
		}
		result.Sizing = sizing
	}

	// Database choice.
	dbChoice, err := interactiveDatabaseChoice(mode)
	if err != nil {
		return nil, err
	}
	result.Database = dbChoice

	// Show plan and confirm.
	if err := interactiveInfraPlan(result, deploymentName); err != nil {
		return nil, err
	}

	return result, nil
}

// showProvisionedCredentials prints a styled box with the connection details
// bolt just provisioned. The user must copy these down — bolt does not store
// secrets on disk.
func showProvisionedCredentials(deploymentName string, outputs *infra.InfraOutputs) {
	if outputs == nil {
		return
	}

	warnStyle := lipgloss.NewStyle().Bold(true).Foreground(amberColor)
	keyStyle := labelStyle
	valStyle := tcStyle
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(amberColor).
		Padding(1, 3)

	fmt.Println()
	fmt.Println(warnStyle.Render("  ⚡ Save these credentials — bolt will not show them again"))
	fmt.Println()

	var lines []string
	lines = append(lines, warnStyle.Render("Provisioned infrastructure for: "+deploymentName))
	lines = append(lines, "")

	if outputs.KubeconfigPath != "" {
		lines = append(lines, keyStyle.Render("Kubeconfig:    ")+valStyle.Render(outputs.KubeconfigPath))
	}
	if outputs.DatabaseURL != "" {
		lines = append(lines, keyStyle.Render("Database URL:  ")+valStyle.Render(maskPassword(outputs.DatabaseURL)))
		lines = append(lines, hintStyle.Render("  (full URL with password passed to TFE automatically)"))
	}
	if outputs.S3Bucket != "" {
		lines = append(lines, keyStyle.Render("Bucket:        ")+valStyle.Render(outputs.S3Bucket))
		lines = append(lines, keyStyle.Render("Region:        ")+valStyle.Render(outputs.S3Region))
	}
	if outputs.S3Endpoint != "" {
		lines = append(lines, keyStyle.Render("S3 endpoint:   ")+valStyle.Render(outputs.S3Endpoint))
	}
	if outputs.S3AccessKeyID != "" {
		lines = append(lines, keyStyle.Render("Access key:    ")+valStyle.Render(outputs.S3AccessKeyID))
	}
	if outputs.S3SecretKey != "" {
		// Show first 8 chars + redacted
		masked := outputs.S3SecretKey
		if len(masked) > 8 {
			masked = masked[:8] + "••••••••"
		}
		lines = append(lines, keyStyle.Render("Secret key:    ")+valStyle.Render(masked))
	}

	fmt.Println(boxStyle.Render(strings.Join(lines, "\n")))
	fmt.Println()
}
