package contracttrace

import (
	"context"
	"fmt"
)

// verifyAnalysisInputs checks the Go environment and selected package sources
// before rereading every file identity component. Go commands are external
// processes, so final input reads must follow them at completion.
func verifyAnalysisInputs(ctx context.Context, root string, build map[string]string, sourceHash string, assets []string, assetHash string, loaded, resolution []LoadedSource, tests bool, tags string) error {
	if err := verifyBuildEnvironment(ctx, root, build); err != nil {
		return err
	}
	if err := verifySelectedPackageSources(ctx, root, build, tests, tags, loaded); err != nil {
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
