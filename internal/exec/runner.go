package runner

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type RunOptions struct {
	Stdout io.Writer
	Stderr io.Writer
	Env    []string // appended to os.Environ()
	Dir    string
}

// Run executes name with args, streaming output live. Use for long-running
// commands (helm install, docker compose up) where progress matters.
func Run(name string, args []string, opts RunOptions) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = opts.Stdout
	if cmd.Stdout == nil {
		cmd.Stdout = os.Stdout
	}
	cmd.Stderr = opts.Stderr
	if cmd.Stderr == nil {
		cmd.Stderr = os.Stderr
	}
	if len(opts.Env) > 0 {
		cmd.Env = append(os.Environ(), opts.Env...)
	}
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

// Output executes name with args and captures stdout. Use for commands whose
// output needs to be parsed (kubectl get pods -o json, helm list -o json).
func Output(name string, args []string, opts RunOptions) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Stderr = opts.Stderr
	if cmd.Stderr == nil {
		cmd.Stderr = os.Stderr
	}
	if len(opts.Env) > 0 {
		cmd.Env = append(os.Environ(), opts.Env...)
	}
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out, nil
}
