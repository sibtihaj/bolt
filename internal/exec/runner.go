package runner

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type RunOptions struct {
	Stdout        io.Writer
	Stderr        io.Writer
	Env           []string      // appended to os.Environ()
	Dir           string
	StderrCapture *bytes.Buffer // if set, stderr is tee'd to both Stderr and this buffer
}

// Run executes name with args, streaming output live. Use for long-running
// commands (helm install, docker compose up) where progress matters.
func Run(name string, args []string, opts RunOptions) error {
	cmd := exec.Command(name, args...)

	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	cmd.Stdout = stdout

	effectiveStderr := opts.Stderr
	if effectiveStderr == nil {
		effectiveStderr = os.Stderr
	}
	if opts.StderrCapture != nil {
		cmd.Stderr = io.MultiWriter(effectiveStderr, opts.StderrCapture)
	} else {
		cmd.Stderr = effectiveStderr
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

// RunWithStdin executes name with args, piping r as stdin and streaming output live.
func RunWithStdin(name string, args []string, r io.Reader, opts RunOptions) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = r

	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	cmd.Stdout = stdout

	effectiveStderr := opts.Stderr
	if effectiveStderr == nil {
		effectiveStderr = os.Stderr
	}
	if opts.StderrCapture != nil {
		cmd.Stderr = io.MultiWriter(effectiveStderr, opts.StderrCapture)
	} else {
		cmd.Stderr = effectiveStderr
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

	effectiveStderr := opts.Stderr
	if effectiveStderr == nil {
		effectiveStderr = os.Stderr
	}
	if opts.StderrCapture != nil {
		cmd.Stderr = io.MultiWriter(effectiveStderr, opts.StderrCapture)
	} else {
		cmd.Stderr = effectiveStderr
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
