package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	trace "github.com/dominicnunez/contract-tracer"
)

func main() { os.Exit(run()) }
func run() int {
	root := flag.String("root", ".", "Go module directory")
	seeds := flag.String("seed", "", "comma-separated function names or fully qualified IDs")
	locations := flag.String("location", "", "comma-separated source locations relative to root (file:line)")
	invariant := flag.String("invariant", "", "initial invariant hypothesis; not automatically proven")
	focus := flag.String("focus", "", "comma-separated explicit contract symbols/resource IDs; explains paths without filtering scope")
	depth := flag.Int("depth", 3, "maximum relationship hops, counting resource nodes")
	budget := flag.Int("max-nodes", 250, "maximum included functions and resources")
	tests := flag.Bool("tests", false, "include test packages")
	tags := flag.String("tags", "", "Go build tags")
	expandCallbacks := flag.Bool("expand-callbacks", false, "expand same-signature CHA callback candidates; can greatly increase noise")
	configPath := flag.String("config", "", "JSON configuration; omitted fields use defaults, [] disables a rule")
	output := flag.String("output", "", "JSON report path; defaults to stdout")
	saveAnalysis := flag.String("save-analysis", "", "save the complete discovered graph; .gz suffix selects gzip JSON")
	resume := flag.String("resume", "", "explore a saved analysis; source snapshot must still match")
	format := flag.String("format", "json", "json or markdown (summary)")
	timeout := flag.Duration("timeout", 3*time.Minute, "cooperative analysis timeout; SSA stages finish before checking cancellation")
	flag.Parse()
	if *resume != "" {
		invalid := ""
		flag.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "root", "config", "tests", "tags", "save-analysis":
				invalid = f.Name
			}
		})
		if invalid != "" {
			fmt.Fprintln(os.Stderr, "-resume cannot be combined with -"+invalid+"; rerun analysis to change its inputs")
			return 1
		}
	}
	if *format != "json" && *format != "markdown" {
		fmt.Fprintln(os.Stderr, "format must be json or markdown")
		return 1
	}
	snapshotPath, snapshotFlag := *saveAnalysis, "save-analysis"
	if *resume != "" {
		snapshotPath, snapshotFlag = *resume, "resume"
	}
	if err := rejectOutputSnapshotAlias(*output, snapshotPath, snapshotFlag); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	config := trace.DefaultConfig()
	if *configPath != "" {
		f, err := os.Open(*configPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		config, err = trace.ReadConfig(f)
		f.Close()
		if err != nil {
			fmt.Fprintln(os.Stderr, "configuration:", err)
			return 1
		}
	}
	symbols := []string{}
	for _, s := range strings.Split(*seeds, ",") {
		if s = strings.TrimSpace(s); s != "" {
			symbols = append(symbols, s)
		}
	}
	positions := []string{}
	focusAnchors := []string{}
	for _, anchor := range strings.Split(*focus, ",") {
		if anchor = strings.TrimSpace(anchor); anchor != "" {
			focusAnchors = append(focusAnchors, anchor)
		}
	}
	for _, p := range strings.Split(*locations, ",") {
		if p = strings.TrimSpace(p); p != "" {
			positions = append(positions, p)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	var r trace.Report
	var err error
	options := trace.Options{Root: *root, Seeds: symbols, Locations: positions, Focus: focusAnchors, Invariant: *invariant, Depth: *depth, MaxNodes: *budget, Tests: *tests, Tags: *tags, Config: config, ExpandCallbacks: *expandCallbacks}
	if *resume != "" {
		f, openErr := os.Open(*resume)
		if openErr != nil {
			fmt.Fprintln(os.Stderr, openErr)
			return 1
		}
		analysis, readErr := trace.ReadAnalysis(f)
		f.Close()
		if readErr != nil {
			fmt.Fprintln(os.Stderr, "saved analysis:", readErr)
			return 1
		}
		r, err = trace.Explore(ctx, analysis, trace.ExploreOptions{Seeds: symbols, Locations: positions, Focus: focusAnchors, Invariant: *invariant, Depth: *depth, MaxNodes: *budget, ExpandCallbacks: *expandCallbacks})
	} else if *saveAnalysis != "" {
		var analysis trace.Analysis
		r, analysis, err = trace.TraceWithAnalysis(ctx, options)
		if err == nil {
			err = saveSnapshot(*saveAnalysis, analysis)
		}
		if err == nil {
			err = rejectOutputSnapshotAlias(*output, *saveAnalysis, "save-analysis")
		}
	} else {
		r, err = trace.Trace(ctx, options)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	data = append(data, '\n')
	if *format == "markdown" {
		data = []byte(trace.Markdown(r))
	}
	if *output == "" {
		_, err = os.Stdout.Write(data)
	} else {
		err = os.WriteFile(*output, data, 0644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func saveSnapshot(destination string, analysis trace.Analysis) error {
	file, err := os.CreateTemp(filepath.Dir(destination), ".contract-analysis-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := trace.WriteAnalysis(file, analysis, strings.EqualFold(filepath.Ext(destination), ".gz")); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, destination)
}
