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
	inputs := analysisInputPaths(analysis)
	for _, destination := range destinations {
		if destination.path == "" {
			continue
		}
		if _, err := inspectOutputPath(destination.path); err != nil {
			return fmt.Errorf("cannot safely inspect -%s %q: %w", destination.flag, destination.path, err)
		}
		rootInput, err := trace.IsFingerprintInputPath(analysis.Root, destination.path)
		if err != nil {
			return fmt.Errorf("cannot safely compare -%s %q with the analyzed source tree: %w", destination.flag, destination.path, err)
		}
		if rootInput {
			return fmt.Errorf("-%s %q would replace or add a file in the analyzed source fingerprint", destination.flag, destination.path)
		}
		for _, input := range inputs {
			same, err := pathsAlias(destination.path, input)
			if err != nil {
				return fmt.Errorf("cannot safely compare -%s %q with analyzed input %q: %w", destination.flag, destination.path, input, err)
			}
			if same {
				return fmt.Errorf("-%s %q aliases an input used by the saved analysis", destination.flag, destination.path)
			}
		}
	}
	return nil
}

func analysisInputPaths(analysis trace.Analysis) []string {
	inputs := make([]string, 0, len(analysis.Coverage.EmbeddedFiles)+len(analysis.Coverage.LoadedSources)+len(analysis.Coverage.ResolutionInputs))
	for _, file := range analysis.Coverage.EmbeddedFiles {
		inputs = append(inputs, filepath.Join(analysis.Root, filepath.FromSlash(file)))
	}
	for _, source := range analysis.Coverage.LoadedSources {
		inputs = append(inputs, filepath.FromSlash(source.Path))
	}
	for _, source := range analysis.Coverage.ResolutionInputs {
		inputs = append(inputs, filepath.FromSlash(source.Path))
	}
	return inputs
}
