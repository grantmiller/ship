package terraform

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/pkg/errors"
	"github.com/replicatedhq/ship/pkg/api"
	"github.com/replicatedhq/ship/pkg/lifecycle/daemon"
	"github.com/replicatedhq/ship/pkg/lifecycle/terraform/tfplan"
	"github.com/replicatedhq/ship/pkg/version"
	"github.com/spf13/viper"
)

const tfSep = "------------------------------------------------------------------------"
const tfNoChanges = "No changes. Infrastructure is up-to-date."

type Terraformer interface {
	Execute(ctx context.Context, release api.Release, step api.Terraform) error
	WithDaemon(d daemon.Daemon) Terraformer
}

type ForkTerraformer struct {
	Logger        log.Logger
	Daemon        daemon.Daemon
	PlanConfirmer tfplan.PlanConfirmer
	Terraform     func() *exec.Cmd
	Viper         *viper.Viper
}

func NewTerraformer(
	logger log.Logger,
	daemon daemon.Daemon,
	planner tfplan.PlanConfirmer,
	viper *viper.Viper,
) Terraformer {
	return &ForkTerraformer{
		Logger:        logger,
		Daemon:        daemon,
		PlanConfirmer: planner,
		Terraform: func() *exec.Cmd {
			return exec.Command("/usr/local/bin/terraform")
		},
		Viper: viper,
	}
}

func (t *ForkTerraformer) WithDaemon(daemon daemon.Daemon) Terraformer {
	return &ForkTerraformer{
		Logger: t.Logger,
		Daemon: daemon,
	}
}

func (t *ForkTerraformer) Execute(ctx context.Context, release api.Release, step api.Terraform) error {
	debug := level.Debug(log.With(t.Logger, "step.type", "terraform", "terraform.phase", "init"))

	assetsPath := filepath.Join("/tmp", "ship-terraform", version.RunAtEpoch, "asset")

	if err := t.init(assetsPath); err != nil {
		return errors.Wrapf(err, "init %s", assetsPath)
	}

	// Observed ~10% flake with errors such as:
	// Error acquiring the state lock: resource temporarily unavailable
	var plan string
	var hasChanges bool
	var err error
	for i := 0; i < 5; i++ {
		plan, hasChanges, err = t.plan(assetsPath)
		if err != nil {
			debug.Log("plan.backoff", i)
			time.Sleep(time.Second * time.Duration(i))
			continue
		}
		if !hasChanges {
			return nil
		}
	}
	if err != nil {
		return err
	}

	if !viper.GetBool("terraform-yes") {
		shouldApply, err := t.PlanConfirmer.ConfirmPlan(ctx, ansiToHTML(plan), release)
		if err != nil {
			return errors.Wrapf(err, "confirm plan for %s", assetsPath)
		}

		if !shouldApply {
			return nil
		}
	}

	// next: apply plan, set progress
	return nil
}

func (t *ForkTerraformer) init(assetsPath string) error {
	debug := level.Debug(log.With(t.Logger, "step.type", "terraform", "terraform.phase", "init"))

	cmd := t.Terraform()
	cmd.Args = append(cmd.Args, "init", "-input=false")
	cmd.Dir = assetsPath

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	debug.Log("stdout", string(out))
	debug.Log("stderr", stderr.String())
	if err != nil {
		return errors.Wrap(err, "exec terraform init")
	}

	return nil
}

// plan returns a human readable plan and a changes-required flag
func (t *ForkTerraformer) plan(assetsPath string) (string, bool, error) {
	debug := level.Debug(log.With(t.Logger, "step.type", "terraform", "terraform.phase", "plan"))
	warn := level.Warn(log.With(t.Logger, "step.type", "terraform", "terraform.phase", "plan"))

	planPath := filepath.Join(filepath.Dir(assetsPath), "plan")
	// we really shouldn't write plan to a file, but this will do for now
	cmd := t.Terraform()
	cmd.Args = append(cmd.Args, "plan", "-input=false", "-out="+planPath)
	cmd.Dir = assetsPath

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	debug.Log("stdout", string(out))
	debug.Log("stderr", stderr.String())
	if err != nil {
		return "", false, errors.Wrap(err, string(out)+"\n"+stderr.String())
		// return "", false, errors.Wrap(err, "exec terraform plan")
	}
	plan := string(out)

	if strings.Contains(plan, tfNoChanges) {
		debug.Log("changes", false)
		return "", false, nil
	}
	debug.Log("changes", true)

	// Drop 1st and 3rd sections with notes on state and how to apply
	sections := strings.Split(plan, tfSep)
	if len(sections) != 3 {
		warn.Log("plan.output.sections", len(sections))
		return plan, true, nil
	}

	return sections[1], true, nil
}