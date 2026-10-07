package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

func main() {
	realGo := os.Getenv("CONTRACT_TRACE_REAL_GO")
	if realGo == "" {
		fatal("missing real go executable")
	}
	args := os.Args[1:]
	if !isBuildEnvironmentQuery(args) {
		command := exec.Command(realGo, args...)
		command.Stdin = os.Stdin
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				os.Exit(exit.ExitCode())
			}
			fatal(err.Error())
		}
		return
	}

	output, err := exec.Command(realGo, args...).Output()
	if err != nil {
		fatal(err.Error())
	}
	countPath := os.Getenv("CONTRACT_TRACE_ENV_COUNT")
	count := 1
	if previous, err := os.ReadFile(countPath); err == nil {
		count, err = strconv.Atoi(string(previous))
		if err != nil {
			fatal("invalid invocation counter")
		}
		count++
	} else if !os.IsNotExist(err) {
		fatal(err.Error())
	}
	if err := os.WriteFile(countPath, []byte(strconv.Itoa(count)), 0600); err != nil {
		fatal(err.Error())
	}

	if count == mustInt("CONTRACT_TRACE_MUTATE_ON") {
		path := os.Getenv("CONTRACT_TRACE_MUTATE_PATH")
		contents := os.Getenv("CONTRACT_TRACE_MUTATE_CONTENT")
		if path == "" {
			fatal("missing mutation path")
		}
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			fatal(err.Error())
		}
		if err := os.WriteFile(os.Getenv("CONTRACT_TRACE_MUTATION_MARKER"), []byte(strconv.Itoa(count)), 0600); err != nil {
			fatal(err.Error())
		}
	}
	if _, err := os.Stdout.Write(output); err != nil {
		fatal(err.Error())
	}
}

func isBuildEnvironmentQuery(args []string) bool {
	if len(args) < 4 || args[0] != "env" || args[1] != "-json" {
		return false
	}
	seen := map[string]bool{}
	for _, arg := range args[2:] {
		seen[arg] = true
	}
	return seen["GOEXPERIMENT"] && seen["GOFIPS140"]
}

func mustInt(name string) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil {
		fatal(fmt.Sprintf("invalid %s: %v", name, err))
	}
	return value
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
