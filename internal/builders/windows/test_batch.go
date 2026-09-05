//go:build ignore

// Command test_batch runs CPython unittest modules one at a time, bounds each
// process, and writes a compact Markdown report plus complete per-module logs.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var (
	ranPattern     = regexp.MustCompile(`(?m)^Ran ([0-9]+) tests?\b`)
	outcomePattern = regexp.MustCompile(`(?m)^(OK(?: \([^\r\n]*\))?|FAILED \([^\r\n]*\))\s*$`)
	modulePattern  = regexp.MustCompile(`^test_[A-Za-z0-9_]+$`)
	testPattern    = regexp.MustCompile(`^test\.test_[A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+)*$`)
	logPattern     = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)
)

type testResult struct {
	module   string
	ran      string
	result   string
	duration time.Duration
	log      string
}

func main() {
	executable := flag.String("exe", "./cpython-go-tests.exe", "interpreter executable")
	moduleFile := flag.String("modules", "internal/builders/windows/test-modules.txt", "whitespace-separated module list")
	tests := flag.String("tests", "", "optional comma- or whitespace-separated unittest selectors")
	timeout := flag.Duration("timeout", 300*time.Second, "timeout for each module")
	output := flag.String("output", "windows-test-results", "result directory")
	label := flag.String("label", runtime.GOARCH, "runner label for the report")
	flag.Parse()

	targets, err := readTargets(*moduleFile, *tests)
	if err != nil {
		fatal(err)
	}
	exe, err := filepath.Abs(*executable)
	if err != nil {
		fatal(err)
	}
	logs := filepath.Join(*output, "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		fatal(err)
	}

	results := make([]testResult, 0, len(targets))
	failures := 0
	for _, target := range targets {
		result := runTarget(exe, target, *timeout, logs)
		results = append(results, result)
		if !strings.HasPrefix(result.result, "OK") {
			failures++
		}
		fmt.Printf("%-24s ran=%-5s %s (%s)\n", result.module, result.ran, result.result, result.duration.Round(time.Millisecond))
	}

	summary := renderSummary(*label, *timeout, results)
	if err := os.WriteFile(filepath.Join(*output, "summary.md"), []byte(summary), 0o644); err != nil {
		fatal(err)
	}
	fmt.Print(summary)
	if failures != 0 {
		os.Exit(1)
	}
}

func readTargets(name, selection string) ([]string, error) {
	if selection != "" {
		selection = strings.ReplaceAll(selection, ",", " ")
		targets := strings.Fields(selection)
		for _, target := range targets {
			if !testPattern.MatchString(target) {
				return nil, fmt.Errorf("invalid unittest selector %q", target)
			}
		}
		if len(targets) == 0 {
			return nil, fmt.Errorf("empty unittest selection")
		}
		return targets, nil
	}

	data, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	modules := strings.Fields(string(data))
	for _, module := range modules {
		if !modulePattern.MatchString(module) {
			return nil, fmt.Errorf("invalid test module %q", module)
		}
	}
	targets := make([]string, len(modules))
	for i, module := range modules {
		targets[i] = "test." + module
	}
	return targets, nil
}

func runTarget(exe, target string, timeout time.Duration, logs string) testResult {
	started := time.Now()
	var output bytes.Buffer
	cmd := exec.Command(exe, "-m", "unittest", "-q", target)
	// An explicit empty pipe is the exec.Command equivalent of `< NUL`. This
	// prevents breakpoint() and pdb from attaching to the runner's console.
	cmd.Stdin = bytes.NewReader(nil)
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Start()
	timedOut := false
	if err == nil {
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		timer := time.NewTimer(timeout)
		select {
		case err = <-done:
			timer.Stop()
		case <-timer.C:
			timedOut = true
			if runtime.GOOS == "windows" {
				_ = exec.Command("taskkill.exe", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
			} else {
				_ = cmd.Process.Kill()
			}
			err = <-done
		}
	}

	duration := time.Since(started)
	text := output.String()
	ran := "-"
	if match := ranPattern.FindStringSubmatch(text); match != nil {
		ran = match[1]
	}
	result := classify(text, err, timedOut)
	display := strings.TrimPrefix(target, "test.")
	logName := logPattern.ReplaceAllString(display, "_") + ".log"
	header := fmt.Sprintf("command: %s\nduration: %s\nresult: %s\n\n", strings.Join(cmd.Args, " "), duration, result)
	if writeErr := os.WriteFile(filepath.Join(logs, logName), []byte(header+text), 0o644); writeErr != nil {
		fatal(writeErr)
	}
	return testResult{module: display, ran: ran, result: result, duration: duration, log: filepath.ToSlash(filepath.Join("logs", logName))}
}

func classify(output string, err error, timedOut bool) string {
	if timedOut {
		return "TIMEOUT"
	}
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "TODOTODO") || strings.HasPrefix(trimmed, "panic:") || strings.HasPrefix(trimmed, "fatal error:") {
			return "CRASH: " + trimmed
		}
	}
	if match := outcomePattern.FindAllStringSubmatch(output, -1); len(match) != 0 {
		outcome := strings.TrimSpace(match[len(match)-1][1])
		if strings.HasPrefix(outcome, "FAILED") || err == nil {
			return outcome
		}
	}
	if err != nil {
		return "CRASH: " + err.Error()
	}
	return "UNKNOWN"
}

func renderSummary(label string, timeout time.Duration, results []testResult) string {
	var summary strings.Builder
	fmt.Fprintf(&summary, "## Windows CPython tests — %s\n\n", markdown(label))
	fmt.Fprintf(&summary, "Per-module timeout: `%s`\n\n", timeout)
	summary.WriteString("| Module | Ran | Result | Duration | Log |\n")
	summary.WriteString("|---|---:|---|---:|---|\n")
	for _, result := range results {
		fmt.Fprintf(
			&summary,
			"| `%s` | %s | %s | %s | `%s` |\n",
			markdown(result.module),
			markdown(result.ran),
			markdown(result.result),
			result.duration.Round(time.Millisecond),
			markdown(result.log),
		)
	}
	return summary.String()
}

func markdown(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.ReplaceAll(value, "\n", " ")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
