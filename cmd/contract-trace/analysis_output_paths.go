package main

import (
	"fmt"
	"path/filepath"

	trace "github.com/dominicnunez/contract-tracer"
)

type analysisArtifactPath struct {
	flag string
	path string
}

func rejectAnalysisInputWrites(analysis trace.Analysis, destinations ...analysisArtifactPath) error {
	return rejectInputWrites(analysis.Root, analysis.Coverage, true, destinations...)
}

func rejectReportInputWrites(report trace.Report, destinations ...analysisArtifactPath) error {
	return rejectInputWrites(report.Root, report.Coverage, false, destinations...)
}

func rejectInputWrites(root string, coverage trace.Coverage, rejectNewInputs bool, destinations ...analysisArtifactPath) error {
	inputs := analysisInputPaths(root, coverage)
	for _, destination := range destinations {
		if destination.path == "" {
			continue
		}
		identity, err := inspectOutputPath(destination.path)
		if err != nil {
			return fmt.Errorf("cannot safely inspect -%s %q: %w", destination.flag, destination.path, err)
		}
		if rejectNewInputs || identity.exists {
			rootInput, err := trace.IsFingerprintInputPath(root, destination.path)
			if err != nil {
				return fmt.Errorf("cannot safely compare -%s %q with the analyzed source tree: %w", destination.flag, destination.path, err)
			}
			if rootInput {
				return fmt.Errorf("-%s %q would replace or add a file in the analyzed source fingerprint", destination.flag, destination.path)
			}
		}
		for _, input := range inputs {
			same, err := pathsAlias(destination.path, input)
			if err != nil {
				return fmt.Errorf("cannot safely compare -%s %q with analyzed input %q: %w", destination.flag, destination.path, input, err)
			}
			if same {
				return fmt.Errorf("-%s %q aliases an analyzed input", destination.flag, destination.path)
			}
		}
	}
	return nil
}

func analysisInputPaths(root string, coverage trace.Coverage) []string {
	inputs := make([]string, 0, len(coverage.EmbeddedFiles)+len(coverage.LoadedSources)+len(coverage.ResolutionInputs))
	for _, file := range coverage.EmbeddedFiles {
		inputs = append(inputs, filepath.Join(root, filepath.FromSlash(file)))
	}
	for _, source := range coverage.LoadedSources {
		inputs = append(inputs, filepath.FromSlash(source.Path))
	}
	for _, source := range coverage.ResolutionInputs {
		inputs = append(inputs, filepath.FromSlash(source.Path))
	}
	return inputs
}
