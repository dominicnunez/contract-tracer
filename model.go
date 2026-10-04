// Package contracttrace discovers investigation scope; it does not prove invariants.
package contracttrace

type Options struct {
	Root            string
	Seeds           []string
	Locations       []string
	Invariant       string
	Focus           []string
	Depth           int
	MaxNodes        int
	Tests           bool
	Tags            string
	ExpandCallbacks bool
	Config          Config
	capture         *Analysis
}
type Config struct {
	EventFields    []string        `json:"event_fields"`
	LifecycleNames []string        `json:"lifecycle_names"`
	SQLMethods     []string        `json:"sql_methods"`
	CallRules      []CallRule      `json:"call_rules,omitempty"`
	SQLDialect     string          `json:"sql_dialect"`
	SQLFiles       []string        `json:"sql_files"`
	StorageScopes  []StorageScope  `json:"storage_scopes,omitempty"`
	LifecycleRules []LifecycleRule `json:"lifecycle_rules,omitempty"`
}

// LifecycleRule describes a configured API role, not a proof of its execution.
// Exactly one selector identifies a resource; argument indexes exclude receivers.
type LifecycleRule struct {
	Symbol    string `json:"symbol"`
	Role      string `json:"role"`
	Namespace string `json:"namespace"`
	Identity  string `json:"identity"`
	Argument  *int   `json:"argument,omitempty"`
	Result    *int   `json:"result,omitempty"`
	Receiver  bool   `json:"receiver,omitempty"`
}
type StorageScope struct {
	Namespace       string   `json:"namespace"`
	GoFiles         []string `json:"go_files,omitempty"`
	SQLFiles        []string `json:"sql_files,omitempty"`
	DatabaseOrigins []string `json:"database_origins,omitempty"`
}
type CallRule struct {
	Symbol          string `json:"symbol"`
	Kind            string `json:"kind"`
	Argument        int    `json:"argument"`
	HandlerArgument *int   `json:"handler_argument,omitempty"`
	Namespace       string `json:"namespace,omitempty"`
}

func DefaultConfig() Config {
	return Config{
		EventFields:    []string{"EventType"},
		LifecycleNames: []string{"recover", "rebuild", "shutdown", "close", "cancel", "stop", "rollback"},
		SQLMethods:     []string{"Exec", "ExecContext", "Query", "QueryContext", "QueryRow", "QueryRowContext", "Prepare", "PrepareContext"},
		SQLDialect:     "sqlite", SQLFiles: []string{"**/*.sql"},
	}
}

type Evidence struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Column  int    `json:"column,omitempty"`
	Snippet string `json:"snippet"`
	Origin  string `json:"origin,omitempty"`
}
type Node struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Kind      string        `json:"kind"`
	Evidence  Evidence      `json:"evidence"`
	Distance  int           `json:"distance"`
	ReachedBy *Relationship `json:"reached_by,omitempty"`
	Relevance *Relevance    `json:"contract_relevance,omitempty"`
}
type Relevance struct {
	Anchor        string        `json:"anchor"`
	Distance      int           `json:"distance"`
	Via           *Relationship `json:"via,omitempty"`
	PathCertainty string        `json:"path_certainty,omitempty"`
}
type Relationship struct {
	From      string   `json:"from"`
	To        string   `json:"to"`
	Kind      string   `json:"kind"`
	Certainty string   `json:"certainty"`
	Evidence  Evidence `json:"evidence"`
	Slot      string   `json:"slot,omitempty"`
	Values    []string `json:"values,omitempty"`
}

type FlowCoverage struct {
	Iterations int  `json:"iterations"`
	Converged  bool `json:"converged"`
	Widened    bool `json:"widened"`
	MaxValues  int  `json:"max_values"`
}
type Boundary struct {
	Node           string   `json:"node,omitempty"`
	Kind           string   `json:"kind"`
	Reason         string   `json:"reason"`
	Evidence       Evidence `json:"evidence"`
	CandidateCount int      `json:"candidate_count,omitempty"`
	Examples       []string `json:"examples,omitempty"`
	Candidates     []string `json:"candidates,omitempty"`
}
type Coverage struct {
	Packages           []string          `json:"packages"`
	Files              []string          `json:"files"`
	ExcludedGoFiles    []string          `json:"excluded_go_files"`
	Build              map[string]string `json:"build"`
	Tests              bool              `json:"tests"`
	Tags               string            `json:"tags"`
	SourceSHA256       string            `json:"source_sha256"`
	LoadedSources      []LoadedSource    `json:"loaded_sources,omitempty"`
	LoadedSourceSHA256 string            `json:"loaded_source_sha256,omitempty"`
	ResolutionInputs   []LoadedSource    `json:"resolution_inputs,omitempty"`
	ResolutionSHA256   string            `json:"resolution_sha256,omitempty"`
	ResolutionScope    string            `json:"resolution_scope,omitempty"`
	Depth              int               `json:"depth"`
	MaxNodes           int               `json:"max_nodes"`
	Truncated          bool              `json:"truncated"`
	ExpandCallbacks    bool              `json:"expand_callbacks"`
	ValueFlow          FlowCoverage      `json:"value_flow"`
	Storage            StorageCoverage   `json:"storage"`
	EmbeddedFiles      []string          `json:"embedded_files"`
}
type StorageCoverage struct {
	Dialect          string              `json:"dialect"`
	Files            []string            `json:"files"`
	ExcludedFiles    []string            `json:"excluded_files"`
	Queries          int                 `json:"distinct_queries"`
	ParsedStatements int                 `json:"parsed_statements"`
	ParseFailures    int                 `json:"parse_failures"`
	Assignments      []StorageAssignment `json:"namespace_assignments,omitempty"`
}
type StorageAssignment struct {
	File      string `json:"file"`
	Namespace string `json:"namespace"`
	Origin    string `json:"origin"`
}
type Report struct {
	Schema           string         `json:"schema"`
	Root             string         `json:"root"`
	Invariant        string         `json:"invariant"`
	Focus            []string       `json:"contract_focus,omitempty"`
	Seeds            []string       `json:"seeds"`
	Nodes            []Node         `json:"nodes"`
	Relationships    []Relationship `json:"relationships"`
	Boundaries       []Boundary     `json:"unresolved_boundaries"`
	Coverage         Coverage       `json:"coverage"`
	Config           Config         `json:"config"`
	ContractComplete bool           `json:"contract_complete"`
}
