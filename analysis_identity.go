package contracttrace

import (
	"context"
	"fmt"
)

// verifyAnalysisInputs checks the Go environment before rereading every file
// identity component. The go command is an external process, so input reads
// must follow it at the analysis completion boundary.
func verifyAnalysisInputs(ctx context.Context, root string, build map[string]string, sourceHash string, assets []string, assetHash string, loaded, resolution []LoadedSource) error {
	if err := verifyBuildEnvironment(ctx, root, build); err != nil {
		return err
	}

	currentAssetHash, err := hashFiles(root, assets)
	if err != nil {
		return err
	}
	if currentAssetHash != assetHash {
		return fmt.Errorf("embedded assets changed during analysis")
	}
	if err := verifyLoadedSources(ctx, loaded); err != nil {
		return err
	}
	if err := verifyLoadedSources(ctx, resolution); err != nil {
		return fmt.Errorf("resolution input check: %w", err)
	}
	currentSourceHash, _, err := fingerprint(root)
	if err != nil {
		return err
	}
	if currentSourceHash != sourceHash {
		return fmt.Errorf("source changed during analysis; rerun on a stable revision")
	}
	return ctx.Err()
}
