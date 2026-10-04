package contracttrace

import (
	"fmt"
	"go/ast"
	"path"
	"sort"
	"strings"
)

func (ix *index) callNamespaces(owner string, call *ast.CallExpr, explicit string, config Config) ([]string, error) {
	var receivers []string
	unknown := false
	if ix.flow != nil {
		receivers = sortedKeys(ix.flow.sqlReceivers[call.Lparen].addresses)
		unknown = ix.flow.sqlReceivers[call.Lparen].sqlUnknown
	}
	return ix.receiverNamespaces(owner, ix.evidence(call.Pos()), receivers, unknown, explicit, config)
}

func (ix *index) databaseOriginBoundaries(config Config) {
	matched := map[string]bool{}
	if ix.flow != nil {
		for key, site := range ix.flow.sqlHandles {
			if strings.HasPrefix(key, "database:") {
				matched[site.owner] = true
			}
		}
	}
	declared := map[string]bool{}
	for _, scope := range config.StorageScopes {
		for _, origin := range scope.DatabaseOrigins {
			declared[origin] = true
		}
	}
	for _, origin := range sortedKeys(declared) {
		if !matched[origin] {
			ix.boundaries = append(ix.boundaries, Boundary{Kind: "unmatched_database_origin", Reason: "no modeled database constructor in configured owner: " + origin})
		}
	}
}

// Namespace declarations apply to constructor owners, never inferred DSNs.
// Possible receiver roots remain a set; this does not correlate invocations.
func (ix *index) receiverNamespaces(owner string, evidence Evidence, receivers []string, unknown bool, explicit string, config Config) ([]string, error) {
	rules := map[string]string{}
	for _, scope := range config.StorageScopes {
		for _, origin := range scope.DatabaseOrigins {
			rules[origin] = scope.Namespace
		}
	}
	if explicit != "" || len(rules) == 0 {
		namespace, err := ix.queryNamespace(owner, evidence, explicit, config)
		return []string{namespace}, err
	}
	namespaces := map[string]bool{}
	missing := unknown || len(receivers) == 0 || ix.flow == nil
	queue := append([]string(nil), receivers...)
	visited := map[string]bool{}
	rootFound := false
	for head := 0; ix.flow != nil && head < len(queue); head++ {
		key := queue[head]
		if visited[key] {
			continue
		}
		visited[key] = true
		site, exists := ix.flow.sqlHandles[key]
		if !exists {
			missing = true
			continue
		}
		if strings.HasPrefix(key, "database:") {
			rootFound = true
			if namespace := rules[site.owner]; namespace != "" {
				namespaces[namespace] = true
				ix.assignStorage(ix.evidence(site.position).File, namespace, "database_origin:"+site.owner)
			} else {
				missing = true
			}
			continue
		}
		if !site.hasParent {
			missing = true
			continue
		}
		parent := site.parent
		missing = missing || site.parentUnknown || parent.sqlUnknown || parent.interfaceUnknown || parent.boundReceiverUnknown
		parents := sortedKeys(parent.addresses)
		if len(parents) == 0 {
			missing = true
		}
		queue = append(queue, parents...)
	}
	missing = missing || !rootFound
	if missing {
		ix.boundaries = append(ix.boundaries, Boundary{Node: owner, Kind: "unresolved_database_namespace", Reason: "receiver candidates include an unknown or unconfigured database origin; known origin namespaces are retained and remaining candidates use file/unscoped fallback", Evidence: evidence})
		namespace, err := ix.queryNamespace(owner, evidence, "", config)
		if err != nil {
			return nil, err
		}
		namespaces[namespace] = true
	}
	return sortedKeys(namespaces), nil
}

func validateFileGlob(pattern string) error {
	if strings.ContainsAny(pattern, "\\:") || strings.HasPrefix(pattern, "/") {
		return fmt.Errorf("file pattern must be a relative slash-separated glob: %q", pattern)
	}
	for _, segment := range strings.Split(pattern, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("invalid file pattern %q", pattern)
		}
		if segment != "**" {
			if _, err := path.Match(segment, ""); err != nil {
				return fmt.Errorf("invalid file pattern %q: %w", pattern, err)
			}
		}
	}
	return nil
}

func scopedStorageFile(file string, sqlFile bool, config Config) (string, error) {
	namespaces := map[string]bool{}
	for _, scope := range config.StorageScopes {
		patterns := scope.GoFiles
		if sqlFile {
			patterns = scope.SQLFiles
		}
		for _, pattern := range patterns {
			if globMatch(pattern, file) {
				namespaces[scope.Namespace] = true
				break
			}
		}
	}
	names := sortedKeys(namespaces)
	if len(names) > 1 {
		return "", fmt.Errorf("ambiguous storage namespace for %s: %s", file, strings.Join(names, ", "))
	}
	if len(names) == 1 {
		return names[0], nil
	}
	return "", nil
}

func (ix *index) assignStorage(file, namespace, origin string) {
	if ix.storageAssignments == nil {
		ix.storageAssignments = map[string]bool{}
	}
	key := file + "\x00" + namespace + "\x00" + origin
	if !ix.storageAssignments[key] {
		ix.storageAssignments[key] = true
		ix.storage.Assignments = append(ix.storage.Assignments, StorageAssignment{file, namespace, origin})
	}
}
func (ix *index) queryNamespace(owner string, evidence Evidence, explicit string, config Config) (string, error) {
	namespace, origin := explicit, "api_rule"
	if namespace == "" {
		var err error
		namespace, err = scopedStorageFile(evidence.File, false, config)
		if err != nil {
			return "", err
		}
		origin = "file_rule"
	}
	if namespace == "" {
		origin = "unscoped"
		if len(config.StorageScopes) > 0 {
			ix.boundaries = append(ix.boundaries, Boundary{Node: owner, Kind: "unresolved_storage_namespace", Reason: "SQL call has no configured database namespace; its unscoped table inventory may alias configured databases, which are not joined automatically", Evidence: evidence})
		}
	}
	ix.assignStorage(evidence.File, namespace, origin)
	return namespace, nil
}
func sortAssignments(storage *StorageCoverage) {
	sort.Slice(storage.Assignments, func(i, j int) bool {
		a, b := storage.Assignments[i], storage.Assignments[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Origin < b.Origin
	})
}
