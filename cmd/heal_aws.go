package cmd

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/sibtihaj/bolt/app/infra"
	awsinfra "github.com/sibtihaj/bolt/app/infra/aws"
	"github.com/sibtihaj/bolt/app/state"
)

// handleAWSProvisionError inspects err for any typed cloud error and routes it
// to the right healing handler.  If healed it retries infra.Provision and
// returns the new outputs.  If the error is unknown it returns it unchanged.
func handleAWSProvisionError(
	ctx context.Context,
	provisionErr error,
	infraCfg *infra.InfraConfig,
	infraState *state.InfraState,
) (*infra.InfraOutputs, error) {
	// ── VPC quota ────────────────────────────────────────────────────────────
	var vpcLimit *awsinfra.VpcLimitExceededError
	if errors.As(provisionErr, &vpcLimit) {
		return healVPCLimit(ctx, vpcLimit, infraCfg, infraState)
	}

	// ── EKS quota ────────────────────────────────────────────────────────────
	var eksQuota *awsinfra.EKSQuotaError
	if errors.As(provisionErr, &eksQuota) {
		return healEKSLimit(ctx, eksQuota, infraCfg, infraState)
	}

	// ── RDS quota ────────────────────────────────────────────────────────────
	var rdsQuota *awsinfra.RDSQuotaError
	if errors.As(provisionErr, &rdsQuota) {
		return healRDSLimit(ctx, rdsQuota, infraCfg, infraState)
	}

	// ── RDS capacity ─────────────────────────────────────────────────────────
	var rdsCap *awsinfra.RDSCapacityError
	if errors.As(provisionErr, &rdsCap) {
		return healRDSCapacity(ctx, rdsCap, infraCfg, infraState)
	}

	// ── EKS cluster exists (external, not created by bolt) ───────────────────
	var eksExists *awsinfra.EKSClusterExistsError
	if errors.As(provisionErr, &eksExists) {
		return healEKSClusterExists(ctx, eksExists, infraCfg, infraState)
	}

	// ── S3 name conflict ─────────────────────────────────────────────────────
	var s3Conflict *awsinfra.S3NameConflictError
	if errors.As(provisionErr, &s3Conflict) {
		return healS3Conflict(ctx, s3Conflict, infraCfg, infraState)
	}

	// ── Credential expiry ────────────────────────────────────────────────────
	var credExpired *infra.CredentialExpiredError
	if errors.As(provisionErr, &credExpired) {
		return healCredentialExpiry(ctx, credExpired, infraCfg, infraState)
	}

	return nil, provisionErr
}

// ── VPC quota ─────────────────────────────────────────────────────────────────

// healVPCLimit lists existing VPCs and lets the user adopt one or delete one.
// Loops on VpcLimitExceededError or VPCValidationError so the picker
// re-appears instead of dropping to the main menu.
func healVPCLimit(
	ctx context.Context,
	vpcLimitErr *awsinfra.VpcLimitExceededError,
	infraCfg *infra.InfraConfig,
	infraState *state.InfraState,
) (*infra.InfraOutputs, error) {
	var prevErr error // validation/adoption failure from a previous iteration

	for {
		// ── Header ───────────────────────────────────────────────────────────
		if prevErr == nil {
			printHealHeader("VPC limit reached",
				"Your AWS account has hit the maximum number of VPCs in this region.")
		} else {
			fmt.Println()
			printInlineError(prevErr)
			fmt.Println(hintStyle.Render("  Please select a different VPC."))
		}

		// ── Fetch VPCs (fresh on every iteration) ────────────────────────────
		fmt.Print(hintStyle.Render("  Fetching existing VPCs…  "))
		vpcs, err := awsinfra.ListVPCs(ctx, vpcLimitErr.Config)
		if err != nil {
			fmt.Println(redMark)
			return nil, err
		}
		fmt.Println(greenMark)
		if len(vpcs) == 0 {
			return nil, fmt.Errorf("VPC limit reached and no existing VPCs found — request a quota increase in the AWS console")
		}

		// ── Picker ───────────────────────────────────────────────────────────
		vpcOpts := make([]huh.Option[string], len(vpcs))
		for i, v := range vpcs {
			vpcOpts[i] = huh.NewOption(v.Label(), v.VPCID)
		}

		var selectedVPC, action string
		if err := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Which VPC would you like to work with?").
					Description("Pick the VPC to use directly or delete to free up a slot.").
					Options(vpcOpts...).
					Value(&selectedVPC),
			),
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("What should bolt do?").
					Options(
						huh.NewOption("Use this VPC  (wire deployment into it, skip creation)", "use"),
						huh.NewOption("Delete this VPC and retry  (clean slate, recreates VPC)", "delete"),
					).
					Value(&action),
			),
		).WithTheme(boltTheme()).Run(); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return nil, fmt.Errorf("cancelled")
			}
			return nil, err
		}

		// ── Apply choice ──────────────────────────────────────────────────────
		if action == "delete" {
			fmt.Printf("\n"+hintStyle.Render("  Deleting VPC %s…  "), selectedVPC)
			if err := awsinfra.DeleteVPC(ctx, vpcLimitErr.Config, selectedVPC); err != nil {
				fmt.Println(redMark)
				return nil, fmt.Errorf("deleting VPC %s: %w", selectedVPC, err)
			}
			fmt.Println(greenMark)
			fmt.Println()
			infraCfg.AWS.ExistingVPCID = ""
		} else {
			infraCfg.AWS.ExistingVPCID = selectedVPC
			fmt.Println()
		}

		// ── Retry provisioning ────────────────────────────────────────────────
		outputs, provErr := infra.Provision(ctx, infraCfg, infraState)
		if provErr == nil {
			return outputs, nil
		}

		// VPC validation failed → loop back with error shown above picker.
		var vpcValErr *awsinfra.VPCValidationError
		if errors.As(provErr, &vpcValErr) {
			prevErr = provErr
			infraCfg.AWS.ExistingVPCID = ""
			continue
		}
		// VPC limit hit again (e.g. delete+retry still at quota) → loop.
		var newLimit *awsinfra.VpcLimitExceededError
		if errors.As(provErr, &newLimit) {
			vpcLimitErr = newLimit
			prevErr = fmt.Errorf("VPC creation failed again — another VPC slot is needed")
			infraCfg.AWS.ExistingVPCID = ""
			continue
		}

		// Any other error propagates to handleAWSProvisionError for further routing.
		return nil, provErr
	}
}

// ── EKS quota ─────────────────────────────────────────────────────────────────

// healEKSLimit lists existing EKS clusters and lets the user delete one.
// Loops if the quota is still hit after the delete+retry.
func healEKSLimit(
	ctx context.Context,
	eksErr *awsinfra.EKSQuotaError,
	infraCfg *infra.InfraConfig,
	infraState *state.InfraState,
) (*infra.InfraOutputs, error) {
	var prevErr error

	for {
		if prevErr == nil {
			printHealHeader("EKS cluster limit reached",
				"Your AWS account has hit the maximum number of EKS clusters in this region.")
		} else {
			fmt.Println()
			printInlineError(prevErr)
			fmt.Println(hintStyle.Render("  Delete another cluster to free up quota."))
		}

		fmt.Print(hintStyle.Render("  Fetching existing EKS clusters…  "))
		clusters, err := awsinfra.ListEKSClusters(ctx, eksErr.Config)
		if err != nil {
			fmt.Println(redMark)
			return nil, err
		}
		fmt.Println(greenMark)
		if len(clusters) == 0 {
			return nil, fmt.Errorf("EKS cluster limit reached and no clusters found — request a quota increase in the AWS console")
		}

		opts := make([]huh.Option[string], len(clusters))
		for i, c := range clusters {
			opts[i] = huh.NewOption(c.Label(), c.Name)
		}

		var selected string
		if err := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Delete which EKS cluster to free up quota?").
					Description("bolt will delete the selected cluster and all its node groups, then retry.").
					Options(opts...).
					Value(&selected),
			),
		).WithTheme(boltTheme()).Run(); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return nil, fmt.Errorf("cancelled")
			}
			return nil, err
		}

		fmt.Printf("\n"+hintStyle.Render("  Deleting EKS cluster %s  (this may take a few minutes)…  "), selected)
		if err := awsinfra.DeleteEKSCluster(ctx, eksErr.Config, selected); err != nil {
			fmt.Println(redMark)
			return nil, fmt.Errorf("deleting EKS cluster %s: %w", selected, err)
		}
		fmt.Println(greenMark)
		fmt.Println()

		outputs, provErr := infra.Provision(ctx, infraCfg, infraState)
		if provErr == nil {
			return outputs, nil
		}

		var newQuota *awsinfra.EKSQuotaError
		if errors.As(provErr, &newQuota) {
			eksErr = newQuota
			prevErr = fmt.Errorf("still at EKS cluster limit — delete another cluster to free a slot")
			continue
		}
		return nil, provErr
	}
}

// ── EKS cluster exists ────────────────────────────────────────────────────────

// healEKSClusterExists presents the user with a choice when an EKS cluster with
// the requested name already exists but was not created by bolt.
// Options: deploy on the existing cluster, or destroy it and provision fresh.
func healEKSClusterExists(
	ctx context.Context,
	existsErr *awsinfra.EKSClusterExistsError,
	infraCfg *infra.InfraConfig,
	infraState *state.InfraState,
) (*infra.InfraOutputs, error) {
	for {
		printHealHeader("EKS cluster name conflict",
			fmt.Sprintf("A cluster named %q already exists in this account (status: %s, k8s %s).\nIt was not created by bolt — you need to decide what to do.",
				existsErr.ClusterName, existsErr.Status, existsErr.Version))

		var choice string
		if err := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("What would you like to do?").
					Options(
						huh.NewOption("Deploy on the existing cluster  (adopt it, skip creation)", "adopt"),
						huh.NewOption("Destroy the existing cluster and provision a new one", "destroy"),
					).
					Value(&choice),
			),
		).WithTheme(boltTheme()).Run(); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return nil, fmt.Errorf("cancelled")
			}
			return nil, err
		}
		fmt.Println()

		if choice == "destroy" {
			infra.StartSpinner(fmt.Sprintf("Deleting EKS cluster %q (this may take several minutes)…", existsErr.ClusterName))
			if err := awsinfra.DeleteEKSCluster(ctx, existsErr.Config, existsErr.ClusterName); err != nil {
				infra.StopSpinner()
				printInlineError(fmt.Errorf("deleting EKS cluster: %w", err))
				continue
			}
			infra.SpinnerSuccess("EKS cluster deleted")
			infraCfg.AWS.ExistingEKSClusterName = ""
		} else {
			infraCfg.AWS.ExistingEKSClusterName = existsErr.ClusterName
		}

		outputs, provErr := infra.Provision(ctx, infraCfg, infraState)
		if provErr == nil {
			return outputs, nil
		}

		// If the cluster still conflicts (e.g. delete didn't complete), loop.
		var newExists *awsinfra.EKSClusterExistsError
		if errors.As(provErr, &newExists) {
			existsErr = newExists
			printInlineError(fmt.Errorf("cluster conflict persists — please try again"))
			infraCfg.AWS.ExistingEKSClusterName = ""
			continue
		}
		return nil, provErr
	}
}

// ── RDS quota ─────────────────────────────────────────────────────────────────

// healRDSLimit lists existing RDS instances and lets the user delete one.
// Loops if the quota is still hit after delete+retry.
func healRDSLimit(
	ctx context.Context,
	rdsErr *awsinfra.RDSQuotaError,
	infraCfg *infra.InfraConfig,
	infraState *state.InfraState,
) (*infra.InfraOutputs, error) {
	var prevErr error

	for {
		if prevErr == nil {
			printHealHeader("RDS instance limit reached",
				"Your AWS account has hit the maximum number of RDS instances.")
		} else {
			fmt.Println()
			printInlineError(prevErr)
			fmt.Println(hintStyle.Render("  Delete another instance to free up quota."))
		}

		fmt.Print(hintStyle.Render("  Fetching existing RDS instances…  "))
		instances, err := awsinfra.ListRDSInstances(ctx, rdsErr.Config)
		if err != nil {
			fmt.Println(redMark)
			return nil, err
		}
		fmt.Println(greenMark)
		if len(instances) == 0 {
			return nil, fmt.Errorf("RDS limit reached and no instances found — request a quota increase in the AWS console")
		}

		opts := make([]huh.Option[string], len(instances))
		for i, db := range instances {
			opts[i] = huh.NewOption(db.Label(), db.InstanceID)
		}

		var selected string
		if err := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Delete which RDS instance to free up quota?").
					Description("The instance will be deleted without a final snapshot.").
					Options(opts...).
					Value(&selected),
			),
		).WithTheme(boltTheme()).Run(); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return nil, fmt.Errorf("cancelled")
			}
			return nil, err
		}

		fmt.Printf("\n"+hintStyle.Render("  Deleting RDS instance %s…  "), selected)
		if err := awsinfra.DeleteRDSPostgres(ctx, rdsErr.Config, selected); err != nil {
			fmt.Println(redMark)
			return nil, fmt.Errorf("deleting RDS instance %s: %w", selected, err)
		}
		fmt.Println(greenMark)
		fmt.Println()

		outputs, provErr := infra.Provision(ctx, infraCfg, infraState)
		if provErr == nil {
			return outputs, nil
		}

		var newQuota *awsinfra.RDSQuotaError
		if errors.As(provErr, &newQuota) {
			rdsErr = newQuota
			prevErr = fmt.Errorf("still at RDS instance limit — delete another instance to free a slot")
			continue
		}
		return nil, provErr
	}
}

// ── RDS capacity ─────────────────────────────────────────────────────────────

// rdsAlternatives maps common instance classes to cheaper/available alternatives.
var rdsAlternatives = map[string][]string{
	"db.r5.large":   {"db.r6g.large", "db.t3.large", "db.r5.xlarge"},
	"db.r5.xlarge":  {"db.r6g.xlarge", "db.r5.2xlarge", "db.t3.xlarge"},
	"db.t3.medium":  {"db.t3.large", "db.t4g.medium", "db.t3.small"},
	"db.t3.large":   {"db.t4g.large", "db.t3.xlarge", "db.r5.large"},
	"db.r6g.large":  {"db.r5.large", "db.t3.large"},
}

// healRDSCapacity suggests alternative instance classes when the requested
// class has no available capacity. Loops if the chosen alternative is also
// unavailable.
func healRDSCapacity(
	ctx context.Context,
	capErr *awsinfra.RDSCapacityError,
	infraCfg *infra.InfraConfig,
	infraState *state.InfraState,
) (*infra.InfraOutputs, error) {
	current := capErr.InstanceClass

	for {
		printHealHeader("RDS capacity unavailable",
			fmt.Sprintf("%s has no available capacity in this region/AZ.", current))

		alts, ok := rdsAlternatives[current]
		if !ok {
			alts = []string{"db.t3.medium", "db.t3.large", "db.r5.large"}
		}
		// Remove the current class from suggestions if it snuck in.
		filtered := alts[:0]
		for _, a := range alts {
			if a != current {
				filtered = append(filtered, a)
			}
		}
		if len(filtered) == 0 {
			filtered = []string{"db.t3.medium", "db.t3.large", "db.r5.large"}
		}

		opts := make([]huh.Option[string], len(filtered))
		for i, a := range filtered {
			opts[i] = huh.NewOption(a, a)
		}

		var selected string
		if err := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title(fmt.Sprintf("Choose an alternative to %s:", current)).
					Description("bolt will retry with the selected instance class.").
					Options(opts...).
					Value(&selected),
			),
		).WithTheme(boltTheme()).Run(); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return nil, fmt.Errorf("cancelled")
			}
			return nil, err
		}

		fmt.Println()
		infraCfg.Sizing.DBClass = selected

		outputs, provErr := infra.Provision(ctx, infraCfg, infraState)
		if provErr == nil {
			return outputs, nil
		}

		var newCap *awsinfra.RDSCapacityError
		if errors.As(provErr, &newCap) {
			current = newCap.InstanceClass
			capErr = newCap
			continue
		}
		return nil, provErr
	}
}

// ── S3 name conflict ──────────────────────────────────────────────────────────

// healS3Conflict lets the user enter a new bucket name when the original is
// globally taken. Loops if the new name is also already taken.
func healS3Conflict(
	ctx context.Context,
	s3Err *awsinfra.S3NameConflictError,
	infraCfg *infra.InfraConfig,
	infraState *state.InfraState,
) (*infra.InfraOutputs, error) {
	takenName := s3Err.BucketName

	for {
		printHealHeader("S3 bucket name taken",
			fmt.Sprintf("%q is already owned by another AWS account — bucket names are globally unique.", takenName))

		suggested := takenName + "-" + randomSuffix(6)
		var newName string

		if err := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Enter a new bucket name:").
					Description("Must be globally unique, 3–63 characters, lowercase letters/numbers/hyphens only.").
					Placeholder(suggested).
					Value(&newName).
					Validate(func(s string) error {
						s = strings.TrimSpace(s)
						if s == "" {
							return nil // use placeholder
						}
						if len(s) < 3 || len(s) > 63 {
							return fmt.Errorf("bucket name must be 3–63 characters")
						}
						return nil
					}),
			),
		).WithTheme(boltTheme()).Run(); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return nil, fmt.Errorf("cancelled")
			}
			return nil, err
		}

		if strings.TrimSpace(newName) == "" {
			newName = suggested
		}
		newName = strings.TrimSpace(newName)
		fmt.Println()

		// Override the S3 bucket name directly so NamePrefix stays unchanged.
		infraCfg.AWS.S3BucketOverride = newName

		outputs, provErr := infra.Provision(ctx, infraCfg, infraState)
		if provErr == nil {
			return outputs, nil
		}

		var newConflict *awsinfra.S3NameConflictError
		if errors.As(provErr, &newConflict) {
			takenName = newConflict.BucketName
			continue
		}
		return nil, provErr
	}
}

// ── Credential expiry ─────────────────────────────────────────────────────────

// healCredentialExpiry re-runs the AWS credential wizard and retries
// provisioning from the top (all Ensure* functions are idempotent).
func healCredentialExpiry(
	ctx context.Context,
	credErr *infra.CredentialExpiredError,
	infraCfg *infra.InfraConfig,
	infraState *state.InfraState,
) (*infra.InfraOutputs, error) {
	_ = credErr
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Foreground(redColor).Bold(true).Render(
		"  ✗  AWS credentials expired mid-provisioning."))
	fmt.Println(hintStyle.Render(
		"  Re-authenticate to continue — bolt will skip already-provisioned resources."))
	fmt.Println()

	newCfg, _, err := interactiveAWSCredentials()
	if err != nil {
		return nil, fmt.Errorf("re-authentication: %w", err)
	}

	// Patch credentials in-place; all other InfraConfig fields stay the same.
	infraCfg.AWS.AccessKeyID = newCfg.AccessKeyID
	infraCfg.AWS.SecretAccessKey = newCfg.SecretAccessKey
	infraCfg.AWS.SessionToken = newCfg.SessionToken
	infraCfg.AWS.AssumeRoleARN = newCfg.AssumeRoleARN

	fmt.Println()
	fmt.Println(hintStyle.Render("  Resuming provisioning…"))
	fmt.Println()
	return infra.Provision(ctx, infraCfg, infraState)
}

// ── shared helpers ────────────────────────────────────────────────────────────

var (
	greenMark = lipgloss.NewStyle().Foreground(greenColor).Render("✓")
	redMark   = lipgloss.NewStyle().Foreground(redColor).Render("✗")
)

func printInlineError(err error) {
	fmt.Println(lipgloss.NewStyle().
		Foreground(redColor).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(redColor).
		Padding(0, 2).
		Render("  ✗  " + err.Error()))
	fmt.Println()
}

func printHealHeader(title, detail string) {
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Foreground(redColor).Bold(true).Render("  ✗  " + title))
	fmt.Println(hintStyle.Render("  " + detail))
	fmt.Println()
}

func randomSuffix(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[r.Intn(len(chars))]
	}
	return string(b)
}
