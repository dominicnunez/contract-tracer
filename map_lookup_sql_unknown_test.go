package contracttrace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMapLookupRetainsSQLHandleUncertainty(t *testing.T) {
	root := t.TempDir()
	write := func(name, source string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/maplookup\n\ngo 1.27.0\n")
	write("app.go", `package maplookup

import (
	"database/sql"
	"context"
	"sync"
)

type dbRecord struct { DB *sql.DB }
type innerRecord struct { DB *sql.DB }
type outerRecord struct { Inner innerRecord }
type namedHandles map[string]*sql.DB
type leaseMap map[string]int
type Lease struct{}
type leaseRecord struct { Lease *Lease }
type contextRecord struct { Ctx context.Context; Ready chan struct{} }
type contextSlot context.Context

func knownTask() {}

func AcquireLease() *Lease { return &Lease{} }
func (*Lease) Close() {}
func AcquireSlot() **Lease {
	var record leaseRecord
	return &record.Lease
}
func DropSlot(**Lease) {}
func AcquireMap() map[string]int { return make(map[string]int) }
func DropMap(map[string]int) {}
func AcquireLeaseMap() leaseMap { return make(leaseMap) }
func (leaseMap) Close() {}

func OpenPrimary() *sql.DB {
	db, _ := sql.Open("driver", "primary")
	return db
}

func lookupHandle[T any](handles map[string]T, key string) T { return handles[key] }

func QueryAfterDelete(mutate bool) {
	db := OpenPrimary()
	handles := map[string]*sql.DB{"known": db}
	if mutate {
		delete(handles, "known")
	}
	handles["known"].Query("SELECT id FROM delete_records")
}

func QueryAfterClear(mutate bool) {
	db := OpenPrimary()
	handles := map[string]*sql.DB{"known": db}
	if mutate {
		clear(handles)
	}
	handles["known"].Query("SELECT id FROM clear_records")
}

func QueryWithMissingKeyAlternative(key string) {
	db := OpenPrimary()
	handles := map[string]*sql.DB{"known": db}
	handles[key].Query("SELECT id FROM dynamic_records")
}

func QueryMissingKey() {
	db := OpenPrimary()
	handles := map[string]*sql.DB{"known": db}
	handles["absent"].Query("SELECT id FROM absent_records")
}

func QueryFromEmptyMap(key string) {
	handles := map[string]*sql.DB{}
	handles[key].Query("SELECT id FROM empty_records")
}

func QueryAfterWildcardStore(key string) {
	db := OpenPrimary()
	handles := map[string]*sql.DB{"known": db}
	replacement := OpenPrimary()
	handles[key] = replacement
	handles["known"].Query("SELECT id FROM wildcard_records")
}

func QueryKnownOnly() {
	db := OpenPrimary()
	handles := map[string]*sql.DB{"known": db}
	handles["known"].Query("SELECT id FROM known_records")
}

func QueryAfterLaterUpdate(update bool) {
	db := OpenPrimary()
	handles := map[string]*sql.DB{"known": db}
	if update {
		replacement := OpenPrimary()
		handles["known"] = replacement
	}
	handles["known"].Query("SELECT id FROM updated_records")
}

func QueryMapPhi(useKnown bool) {
	var handles map[string]*sql.DB
	if useKnown {
		db := OpenPrimary()
		handles = map[string]*sql.DB{"known": db}
	} else {
		handles = map[string]*sql.DB{}
	}
	handles["known"].Query("SELECT id FROM phi_records")
}

func QueryNilMapPhi(useKnown bool) {
	var handles map[string]*sql.DB
	if useKnown {
		db := OpenPrimary()
		handles = map[string]*sql.DB{"known": db}
	}
	handles["known"].Query("SELECT id FROM nil_phi_records")
}

func QueryRecordFieldAlternative(key string) {
	db := OpenPrimary()
	records := map[string]dbRecord{"known": {DB: db}}
	records[key].DB.Query("SELECT id FROM record_alternatives")
}

func QueryNestedRecordAlternative(key string) {
	db := OpenPrimary()
	records := map[string]outerRecord{"known": {Inner: innerRecord{DB: db}}}
	records[key].Inner.DB.Query("SELECT id FROM nested_record_alternatives")
}

func QueryArrayRecordAlternative(key string) {
	db := OpenPrimary()
	records := map[string][1]innerRecord{"known": {{DB: db}}}
	records[key][0].DB.Query("SELECT id FROM array_record_alternatives")
}

func QueryArrayDBAlternative(key string) {
	db := OpenPrimary()
	handles := map[string][1]*sql.DB{"known": {db}}
	handles[key][0].Query("SELECT id FROM array_db_alternatives")
}

func querySpilledArrayDynamic(array [1]*sql.DB) {
	array[0].Query("SELECT id FROM spilled_array_alternatives")
}

func querySpilledArrayKnown(array [1]*sql.DB) {
	array[0].Query("SELECT id FROM spilled_array_known")
}

func querySpilledArrayPointerDynamic(array *[1]*sql.DB) {
	array[0].Query("SELECT id FROM spilled_array_pointer_alternatives")
}

func querySpilledArrayPointerKnown(array *[1]*sql.DB) {
	array[0].Query("SELECT id FROM spilled_array_pointer_known")
}

func querySpilledRecordDynamic(record *dbRecord) {
	record.DB.Query("SELECT id FROM spilled_record_alternatives")
}

func querySpilledRecordKnown(record *dbRecord) {
	record.DB.Query("SELECT id FROM spilled_record_known")
}

func QuerySpilledArrayDBAlternative(key string) {
	db := OpenPrimary()
	handles := map[string][1]*sql.DB{"known": {db}}
	array := handles[key]
	querySpilledArrayDynamic(array)
}

func QuerySpilledArrayDBKnown() {
	db := OpenPrimary()
	handles := map[string][1]*sql.DB{"known": {db}}
	array := handles["known"]
	querySpilledArrayKnown(array)
}

func QuerySpilledArrayPointerAlternative(key string) {
	db := OpenPrimary()
	handles := map[string][1]*sql.DB{"known": {db}}
	array := handles[key]
	querySpilledArrayPointerDynamic(&array)
}

func QuerySpilledArrayPointerKnown() {
	db := OpenPrimary()
	handles := map[string][1]*sql.DB{"known": {db}}
	array := handles["known"]
	querySpilledArrayPointerKnown(&array)
}

func QuerySpilledRecordAlternative(key string) {
	db := OpenPrimary()
	records := map[string]dbRecord{"known": {DB: db}}
	record := records[key]
	querySpilledRecordDynamic(&record)
}

func QuerySpilledRecordKnown() {
	db := OpenPrimary()
	records := map[string]dbRecord{"known": {DB: db}}
	record := records["known"]
	querySpilledRecordKnown(&record)
}

func QueryNilParentEmptyMap(key string) {
	records := map[string]*dbRecord{}
	records[key].DB.Query("SELECT id FROM nil_parent_records")
}

func QueryNamedMapAlternative(key string) {
	db := OpenPrimary()
	handles := namedHandles{"known": db}
	handles[key].Query("SELECT id FROM named_map_alternatives")
}

func QueryGenericMapAlternative(key string) {
	db := OpenPrimary()
	handles := map[string]*sql.DB{"known": db}
	lookupHandle(handles, key).Query("SELECT id FROM generic_map_alternatives")
}

func QueryNestedMapAlternative(key string) {
	db := OpenPrimary()
	inner := map[string]*sql.DB{"known": db}
	outer := map[string]map[string]*sql.DB{"known": inner}
	outer[key]["known"].Query("SELECT id FROM nested_map_alternatives")
}

func QueryDirect() {
	db := OpenPrimary()
	db.Query("SELECT id FROM direct_records")
}

func QueryTaskMapNilPhi(useKnown bool) {
	var tasks map[string]func()
	if useKnown {
		tasks = map[string]func(){"known": knownTask}
	}
	var group sync.WaitGroup
	group.Go(tasks["known"])
}

func QueryTaskMapKnownOnly() {
	tasks := map[string]func(){"known": knownTask}
	var group sync.WaitGroup
	group.Go(tasks["known"])
}

func QueryNestedTaskMapPhi(useKnown bool) {
	var tasks map[string]map[string]func()
	if useKnown {
		tasks = map[string]map[string]func(){"known": {"task": knownTask}}
	}
	var group sync.WaitGroup
	group.Go(tasks["known"]["task"])
}

func QueryNestedTaskMapKnownOnly() {
	tasks := map[string]map[string]func(){"known": {"task": knownTask}}
	var group sync.WaitGroup
	group.Go(tasks["known"]["task"])
}

func QueryArrayTaskMapNilPhi(useKnown bool) {
	var tasks map[string][1]map[string]func()
	if useKnown {
		inner := map[string]func(){"task": knownTask}
		tasks = map[string][1]map[string]func(){"known": {inner}}
	}
	var group sync.WaitGroup
	group.Go(tasks["known"][0]["task"])
}

func QueryArrayTaskMapKnownOnly() {
	inner := map[string]func(){"task": knownTask}
	tasks := map[string][1]map[string]func(){"known": {inner}}
	var group sync.WaitGroup
	group.Go(tasks["known"][0]["task"])
}

func QueryPointerMapChildAlternative(key string) {
	callbacks := map[string]func(){"known": knownTask}
	maps := map[string]*map[string]func(){"known": &callbacks}
	child := maps[key]
	var group sync.WaitGroup
	group.Go((*child)["known"])
}

func ObserveDynamicContextRecord(key string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	records := map[string]contextRecord{"known": {Ctx: ctx, Ready: ready}}
	<-records[key].Ctx.Done()
	<-records[key].Ready
}

func ObserveKnownContextRecord() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	records := map[string]contextRecord{"known": {Ctx: ctx, Ready: ready}}
	<-records["known"].Ctx.Done()
	<-records["known"].Ready
}

func ObserveDynamicConvertedContextRecord(key string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	records := map[string]contextRecord{"known": {Ctx: ctx, Ready: ready}}
	record := records[key]
	slot := (*contextSlot)(&record.Ctx)
	<-(*slot).Done()
	<-record.Ready
}

func ObserveKnownConvertedContextRecord() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	records := map[string]contextRecord{"known": {Ctx: ctx, Ready: ready}}
	record := records["known"]
	slot := (*contextSlot)(&record.Ctx)
	<-(*slot).Done()
	<-record.Ready
}

func QueryLeaseMapNilPhi(useKnown bool) {
	var leases map[string]*Lease
	if useKnown {
		leases = map[string]*Lease{"known": AcquireLease()}
	}
	leases["known"].Close()
}

func QueryLeaseMapKnownOnly() {
	leases := map[string]*Lease{"known": AcquireLease()}
	leases["known"].Close()
}

func QueryLeaseRecordAlternative(key string) {
	records := map[string]leaseRecord{"known": {Lease: AcquireLease()}}
	records[key].Lease.Close()
}

func QueryLeaseRecordKnownOnly() {
	records := map[string]leaseRecord{"known": {Lease: AcquireLease()}}
	records["known"].Lease.Close()
}

func closeDynamicLeaseSlot(slot **Lease) {
	lease := *slot
	lease.Close()
}

func closeKnownLeaseSlot(slot **Lease) {
	lease := *slot
	lease.Close()
}

func UseKnownSlot() {
	DropSlot(AcquireSlot())
}

func QueryLeaseRecordSlotAlternative(key string) {
	records := map[string]leaseRecord{"known": {Lease: AcquireLease()}}
	record := records[key]
	closeDynamicLeaseSlot(&record.Lease)
}

func QueryLeaseRecordSlotKnownOnly() {
	records := map[string]leaseRecord{"known": {Lease: AcquireLease()}}
	record := records["known"]
	closeKnownLeaseSlot(&record.Lease)
}

func ReleaseMapNilPhi(useNil bool) {
	handle := AcquireMap()
	if useNil {
		handle = nil
	}
	DropMap(handle)
}

func ReleaseMapKnownOnly() {
	handle := AcquireMap()
	DropMap(handle)
}

func ReleaseBoundMapNilPhi(useNil bool) {
	handle := AcquireLeaseMap()
	if useNil {
		handle = nil
	}
	closeHandle := handle.Close
	closeHandle()
}

func ReleaseBoundMapKnownOnly() {
	handle := AcquireLeaseMap()
	closeHandle := handle.Close
	closeHandle()
}
`)

	config := DefaultConfig()
	config.StorageScopes = []StorageScope{{Namespace: "primary", DatabaseOrigins: []string{"example.com/maplookup::OpenPrimary"}}}
	zero := 0
	config.LifecycleRules = []LifecycleRule{
		{Symbol: "example.com/maplookup::AcquireLease", Role: "acquire", Namespace: "leases", Identity: "origin", Result: &zero},
		{Symbol: "example.com/maplookup::Lease.Close", Role: "release", Namespace: "leases", Identity: "origin", Receiver: true},
		{Symbol: "example.com/maplookup::AcquireMap", Role: "acquire", Namespace: "map-handles", Identity: "origin", Result: &zero},
		{Symbol: "example.com/maplookup::DropMap", Role: "release", Namespace: "map-handles", Identity: "origin", Argument: &zero},
		{Symbol: "example.com/maplookup::AcquireLeaseMap", Role: "acquire", Namespace: "bound-map-handles", Identity: "origin", Result: &zero},
		{Symbol: "example.com/maplookup::leaseMap.Close", Role: "release", Namespace: "bound-map-handles", Identity: "origin", Receiver: true},
		{Symbol: "example.com/maplookup::AcquireSlot", Role: "acquire", Namespace: "lease-slots", Identity: "origin", Result: &zero},
		{Symbol: "example.com/maplookup::DropSlot", Role: "release", Namespace: "lease-slots", Identity: "origin", Argument: &zero},
	}
	seeds := []string{
		"QueryAfterDelete", "QueryAfterClear", "QueryWithMissingKeyAlternative", "QueryMissingKey",
		"QueryFromEmptyMap", "QueryAfterWildcardStore", "QueryKnownOnly", "QueryAfterLaterUpdate", "QueryMapPhi",
		"QueryNilMapPhi", "QueryRecordFieldAlternative", "QueryNestedRecordAlternative",
		"QueryArrayRecordAlternative", "QueryArrayDBAlternative", "QueryNilParentEmptyMap", "QueryNamedMapAlternative",
		"QuerySpilledArrayDBAlternative", "QuerySpilledArrayDBKnown", "QuerySpilledArrayPointerAlternative", "QuerySpilledArrayPointerKnown", "QuerySpilledRecordAlternative", "QuerySpilledRecordKnown",
		"QueryGenericMapAlternative", "QueryDirect",
		"QueryTaskMapNilPhi", "QueryTaskMapKnownOnly",
		"QueryLeaseMapNilPhi", "QueryLeaseMapKnownOnly",
		"QueryLeaseRecordAlternative", "QueryLeaseRecordKnownOnly",
		"QueryLeaseRecordSlotAlternative", "QueryLeaseRecordSlotKnownOnly",
		"UseKnownSlot",
		"QueryNestedTaskMapPhi", "QueryNestedTaskMapKnownOnly", "QueryArrayTaskMapNilPhi", "QueryArrayTaskMapKnownOnly",
		"QueryPointerMapChildAlternative",
		"ObserveDynamicContextRecord", "ObserveKnownContextRecord", "ObserveDynamicConvertedContextRecord", "ObserveKnownConvertedContextRecord",
		"ReleaseMapNilPhi", "ReleaseMapKnownOnly",
		"ReleaseBoundMapNilPhi", "ReleaseBoundMapKnownOnly",
		"QueryNestedMapAlternative",
	}
	report, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: seeds, Depth: 5, MaxNodes: 300, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	if report.Coverage.Truncated || report.ContractComplete {
		t.Fatalf("map probe must be untruncated and leave analysis incomplete: truncated=%t complete=%t", report.Coverage.Truncated, report.ContractComplete)
	}
	var encoded bytes.Buffer
	if err := WriteAnalysis(&encoded, analysis, true); err != nil {
		t.Fatal(err)
	}
	saved, err := ReadAnalysis(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), saved, ExploreOptions{Seeds: seeds, Depth: 5, MaxNodes: 300})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Coverage.Truncated || resumed.ContractComplete {
		t.Fatalf("resumed map probe must be untruncated and incomplete: truncated=%t complete=%t", resumed.Coverage.Truncated, resumed.ContractComplete)
	}

	for _, current := range []Report{report, resumed} {
		for _, tc := range []struct {
			owner         string
			contextCall   string
			channelRead   string
			wantUncertain bool
		}{
			{"ObserveDynamicContextRecord", "records[key].Ctx.Done()", "records[key].Ready", true},
			{"ObserveKnownContextRecord", "records[\"known\"].Ctx.Done()", "records[\"known\"].Ready", false},
			{"ObserveDynamicConvertedContextRecord", "(*slot).Done()", "record.Ready", true},
			{"ObserveKnownConvertedContextRecord", "(*slot).Done()", "record.Ready", false},
		} {
			owner := "example.com/maplookup::" + tc.owner
			contextSite := ""
			channelSite := ""
			for _, edge := range current.Relationships {
				if edge.From == owner && edge.Kind == "context_observe" && edge.Evidence.File == "app.go" && strings.Contains(edge.Evidence.Snippet, tc.contextCall) {
					contextSite = edge.To
				}
				if edge.From == owner && edge.Kind == "channel_receive" && edge.Evidence.File == "app.go" && strings.Contains(edge.Evidence.Snippet, tc.channelRead) {
					channelSite = edge.To
				}
			}
			if contextSite == "" || channelSite == "" {
				t.Errorf("%s lost known context/channel map-record candidates: context=%q channel=%q", tc.owner, contextSite, channelSite)
			}
			contextUnknown, channelUnknown := false, false
			for _, boundary := range current.Boundaries {
				if boundary.Node == owner && boundary.Kind == "unresolved_context" && boundary.Evidence.File == "app.go" && strings.Contains(boundary.Evidence.Snippet, tc.contextCall) {
					contextUnknown = true
				}
				if boundary.Node == owner && boundary.Kind == "unresolved_channel" && boundary.Evidence.File == "app.go" && strings.Contains(boundary.Evidence.Snippet, tc.channelRead) {
					channelUnknown = true
				}
			}
			if contextUnknown != tc.wantUncertain || channelUnknown != tc.wantUncertain {
				t.Errorf("%s map-record zero alternative context/channel uncertainty=(%t,%t), want both %t", tc.owner, contextUnknown, channelUnknown, tc.wantUncertain)
			}
		}
		for _, tc := range []struct {
			owner       string
			table       string
			wantKnown   bool
			wantUnknown bool
		}{
			{"QueryAfterDelete", "delete_records", true, true},
			{"QueryAfterClear", "clear_records", true, true},
			{"QueryWithMissingKeyAlternative", "dynamic_records", true, true},
			{"QueryMissingKey", "absent_records", false, true},
			{"QueryFromEmptyMap", "empty_records", false, true},
			{"QueryAfterWildcardStore", "wildcard_records", true, true},
			{"QueryKnownOnly", "known_records", true, false},
			{"QueryAfterLaterUpdate", "updated_records", true, true},
			{"QueryMapPhi", "phi_records", true, true},
			{"QueryNilMapPhi", "nil_phi_records", true, true},
			{"QueryRecordFieldAlternative", "record_alternatives", true, true},
			{"QueryNestedRecordAlternative", "nested_record_alternatives", true, true},
			{"QueryArrayRecordAlternative", "array_record_alternatives", true, true},
			{"QueryArrayDBAlternative", "array_db_alternatives", true, true},
			{"querySpilledArrayDynamic", "spilled_array_alternatives", true, true},
			{"querySpilledArrayKnown", "spilled_array_known", true, false},
			{"querySpilledArrayPointerDynamic", "spilled_array_pointer_alternatives", true, true},
			{"querySpilledArrayPointerKnown", "spilled_array_pointer_known", true, false},
			{"querySpilledRecordDynamic", "spilled_record_alternatives", true, true},
			{"querySpilledRecordKnown", "spilled_record_known", true, false},
			{"QueryNilParentEmptyMap", "nil_parent_records", false, true},
			{"QueryNamedMapAlternative", "named_map_alternatives", true, true},
			{"QueryGenericMapAlternative", "generic_map_alternatives", true, true},
			{"QueryNestedMapAlternative", "nested_map_alternatives", true, true},
			{"QueryDirect", "direct_records", true, false},
		} {
			owner := "example.com/maplookup::" + tc.owner
			known := hasRelationship(current, owner, "table:primary:"+tc.table, "sql_read", "app.go", 0)
			if known != tc.wantKnown {
				t.Errorf("%s known SQL edge to %s: got %t, want %t", tc.owner, tc.table, known, tc.wantKnown)
			}
			uncertain := false
			operation := ""
			for _, boundary := range current.Boundaries {
				if boundary.Kind == "unresolved_sql_handle" && boundary.Evidence.File == "app.go" && strings.Contains(boundary.Evidence.Snippet, tc.table) {
					uncertain = true
					operation = boundary.Node
				}
			}
			if tc.wantUnknown && !uncertain {
				t.Errorf("%s lost its source-backed unknown map-handle alternative", tc.owner)
			} else if !tc.wantUnknown && uncertain {
				t.Errorf("closed known-only lookup acquired SQL handle uncertainty: %s", tc.owner)
			}
			if uncertain && !tc.wantKnown {
				for _, edge := range current.Relationships {
					if edge.Kind == "sql_receiver" && edge.From == operation && strings.HasPrefix(edge.To, "database:") {
						t.Errorf("%s invented a database receiver origin from a missing map value: %+v", tc.owner, edge)
					}
				}
			}
		}
		knownTaskBoundaryState(t, current, root)
		nestedTaskMapBoundaryState(t, current, root)
		leaseMapBoundaryState(t, current, root)
		leaseFieldMapBoundaryState(t, current, root)
		leaseSlotMapBoundaryState(t, current, root)
		knownSlotState(t, current, root)
		pointerMapChildState(t, current, root)
		releaseMapBoundaryState(t, current, root)
		boundMapBoundaryState(t, current, root)
	}
}

func leaseMapBoundaryState(t *testing.T, report Report, root string) {
	t.Helper()
	for _, test := range []struct {
		name          string
		wantUncertain bool
	}{
		{"QueryLeaseMapNilPhi", true},
		{"QueryLeaseMapKnownOnly", false},
	} {
		acquired := map[string]bool{}
		for _, edge := range report.Relationships {
			if edge.From == "example.com/maplookup::"+test.name && edge.Kind == "lifecycle_acquire" && strings.Contains(edge.Evidence.Snippet, "AcquireLease()") {
				acquired[edge.To] = true
			}
		}
		line := sourceLineInFunction(t, root, "app.go", test.name, "leases[\"known\"].Close()")
		released := false
		for _, edge := range report.Relationships {
			if edge.From == "example.com/maplookup::"+test.name && edge.Kind == "lifecycle_release" && acquired[edge.To] && edge.Evidence.File == "app.go" && edge.Evidence.Line == line && strings.Contains(edge.Evidence.Snippet, "leases[\"known\"].Close()") {
				released = true
			}
		}
		if len(acquired) == 0 || !released {
			t.Errorf("%s lost configured lease acquire/release identity", test.name)
		}
		uncertain := false
		for _, boundary := range report.Boundaries {
			if boundary.Kind == "unresolved_lifecycle_resource" && boundary.Node == "example.com/maplookup::"+test.name && boundary.Evidence.File == "app.go" && boundary.Evidence.Line == line && strings.Contains(boundary.Evidence.Snippet, "leases[\"known\"].Close()") {
				uncertain = true
			}
		}
		if uncertain != test.wantUncertain {
			t.Errorf("%s unresolved lease alternative=%t, want %t", test.name, uncertain, test.wantUncertain)
		}
	}
}

func leaseFieldMapBoundaryState(t *testing.T, report Report, root string) {
	t.Helper()
	for _, test := range []struct {
		name          string
		wantUncertain bool
	}{
		{"QueryLeaseRecordAlternative", true},
		{"QueryLeaseRecordKnownOnly", false},
	} {
		snippet := "records[\"known\"].Lease.Close()"
		if test.wantUncertain {
			snippet = "records[key].Lease.Close()"
		}
		line := sourceLineInFunction(t, root, "app.go", test.name, snippet)
		acquired := map[string]bool{}
		for _, edge := range report.Relationships {
			if edge.From == "example.com/maplookup::"+test.name && edge.Kind == "lifecycle_acquire" && strings.Contains(edge.Evidence.Snippet, "AcquireLease()") {
				acquired[edge.To] = true
			}
		}
		released, uncertain := false, false
		for _, edge := range report.Relationships {
			if edge.From == "example.com/maplookup::"+test.name && edge.Kind == "lifecycle_release" && acquired[edge.To] && edge.Evidence.File == "app.go" && edge.Evidence.Line == line && strings.Contains(edge.Evidence.Snippet, snippet) {
				released = true
			}
		}
		for _, boundary := range report.Boundaries {
			if boundary.Kind == "unresolved_lifecycle_resource" && boundary.Node == "example.com/maplookup::"+test.name && boundary.Evidence.File == "app.go" && boundary.Evidence.Line == line && strings.Contains(boundary.Evidence.Snippet, snippet) {
				uncertain = strings.Contains(boundary.Reason, "nil") || strings.Contains(boundary.Reason, "unresolved resource")
			}
		}
		if len(acquired) == 0 || !released {
			t.Errorf("%s lost configured Lease acquire/release identity", test.name)
		}
		if uncertain != test.wantUncertain {
			t.Errorf("%s unresolved nil Lease receiver=%t, want %t", test.name, uncertain, test.wantUncertain)
		}
	}
}

func leaseSlotMapBoundaryState(t *testing.T, report Report, root string) {
	t.Helper()
	for _, test := range []struct {
		caller        string
		helper        string
		wantUncertain bool
	}{
		{"QueryLeaseRecordSlotAlternative", "closeDynamicLeaseSlot", true},
		{"QueryLeaseRecordSlotKnownOnly", "closeKnownLeaseSlot", false},
	} {
		acquired := map[string]bool{}
		for _, edge := range report.Relationships {
			if edge.From == "example.com/maplookup::"+test.caller && edge.Kind == "lifecycle_acquire" && strings.Contains(edge.Evidence.Snippet, "AcquireLease()") {
				acquired[edge.To] = true
			}
		}
		line := sourceLineInFunction(t, root, "app.go", test.helper, "lease.Close()")
		released, uncertain := false, false
		for _, edge := range report.Relationships {
			if edge.From == "example.com/maplookup::"+test.helper && edge.Kind == "lifecycle_release" && acquired[edge.To] && edge.Evidence.File == "app.go" && edge.Evidence.Line == line && strings.Contains(edge.Evidence.Snippet, "lease.Close()") {
				released = true
			}
		}
		for _, boundary := range report.Boundaries {
			if boundary.Kind == "unresolved_lifecycle_resource" && boundary.Node == "example.com/maplookup::"+test.helper && boundary.Evidence.File == "app.go" && boundary.Evidence.Line == line && strings.Contains(boundary.Evidence.Snippet, "lease.Close()") {
				uncertain = strings.Contains(boundary.Reason, "nil") || strings.Contains(boundary.Reason, "unresolved resource")
			}
		}
		if len(acquired) == 0 || !released {
			t.Errorf("%s lost configured Lease acquire/release identity through **Lease", test.caller)
		}
		if uncertain != test.wantUncertain {
			t.Errorf("%s unresolved selected Lease receiver=%t, want %t", test.caller, uncertain, test.wantUncertain)
		}
	}
}

func knownSlotState(t *testing.T, report Report, root string) {
	t.Helper()
	owner := "example.com/maplookup::UseKnownSlot"
	line := sourceLineInFunction(t, root, "app.go", "UseKnownSlot", "DropSlot(AcquireSlot())")
	acquired := map[string]bool{}
	for _, edge := range report.Relationships {
		if edge.From == owner && edge.Kind == "lifecycle_acquire" && strings.Contains(edge.Evidence.Snippet, "AcquireSlot()") {
			acquired[edge.To] = true
		}
	}
	released, unresolved := false, false
	for _, edge := range report.Relationships {
		if edge.From == owner && edge.Kind == "lifecycle_release" && acquired[edge.To] && edge.Evidence.File == "app.go" && edge.Evidence.Line == line && strings.Contains(edge.Evidence.Snippet, "DropSlot(AcquireSlot())") {
			released = true
		}
	}
	for _, boundary := range report.Boundaries {
		if boundary.Kind == "unresolved_lifecycle_resource" && boundary.Node == owner && boundary.Evidence.File == "app.go" && boundary.Evidence.Line == line {
			unresolved = true
		}
	}
	if len(acquired) == 0 || !released {
		t.Error("known **Lease slot lost its acquire/release identity")
	}
	if unresolved {
		t.Error("known **Lease slot address was mistaken for its nil Lease payload")
	}
}

func releaseMapBoundaryState(t *testing.T, report Report, root string) {
	t.Helper()
	for _, test := range []struct {
		name          string
		wantUncertain bool
	}{
		{"ReleaseMapNilPhi", true},
		{"ReleaseMapKnownOnly", false},
	} {
		snippet := "DropMap(handle)"
		line := sourceLineInFunction(t, root, "app.go", test.name, snippet)
		acquired := map[string]bool{}
		for _, edge := range report.Relationships {
			if edge.From == "example.com/maplookup::"+test.name && edge.Kind == "lifecycle_acquire" && strings.Contains(edge.Evidence.Snippet, "AcquireMap()") {
				acquired[edge.To] = true
			}
		}
		released, uncertain := false, false
		for _, edge := range report.Relationships {
			if edge.From == "example.com/maplookup::"+test.name && edge.Kind == "lifecycle_release" && acquired[edge.To] && edge.Evidence.File == "app.go" && edge.Evidence.Line == line && strings.Contains(edge.Evidence.Snippet, snippet) {
				released = true
			}
		}
		for _, boundary := range report.Boundaries {
			if boundary.Kind == "unresolved_lifecycle_resource" && boundary.Node == "example.com/maplookup::"+test.name && boundary.Evidence.File == "app.go" && boundary.Evidence.Line == line && strings.Contains(boundary.Evidence.Snippet, snippet) {
				uncertain = true
			}
		}
		if len(acquired) == 0 || !released {
			t.Errorf("%s lost configured map lease acquire/release identity", test.name)
		}
		if uncertain != test.wantUncertain {
			t.Errorf("%s unresolved direct map-handle alternative=%t, want %t", test.name, uncertain, test.wantUncertain)
		}
	}
}

func boundMapBoundaryState(t *testing.T, report Report, root string) {
	t.Helper()
	for _, test := range []struct {
		name          string
		wantUncertain bool
	}{
		{"ReleaseBoundMapNilPhi", true},
		{"ReleaseBoundMapKnownOnly", false},
	} {
		snippet := "closeHandle()"
		line := sourceLineInFunction(t, root, "app.go", test.name, snippet)
		acquired := map[string]bool{}
		for _, edge := range report.Relationships {
			if edge.From == "example.com/maplookup::"+test.name && edge.Kind == "lifecycle_acquire" && strings.Contains(edge.Evidence.Snippet, "AcquireLeaseMap()") {
				acquired[edge.To] = true
			}
		}
		released, uncertain := false, false
		for _, edge := range report.Relationships {
			if edge.From == "example.com/maplookup::"+test.name && edge.Kind == "lifecycle_release" && acquired[edge.To] && edge.Evidence.File == "app.go" && edge.Evidence.Line == line && strings.Contains(edge.Evidence.Snippet, snippet) {
				released = true
			}
		}
		for _, boundary := range report.Boundaries {
			if boundary.Kind == "unresolved_lifecycle_resource" && boundary.Node == "example.com/maplookup::"+test.name && boundary.Evidence.File == "app.go" && boundary.Evidence.Line == line && strings.Contains(boundary.Evidence.Snippet, snippet) {
				uncertain = strings.Contains(boundary.Reason, "nil") || strings.Contains(boundary.Reason, "unresolved resource")
			}
			if test.wantUncertain && boundary.Node == "example.com/maplookup::"+test.name && boundary.Evidence.File == "app.go" && boundary.Evidence.Line == line && strings.Contains(boundary.Evidence.Snippet, snippet) && boundary.Kind == "unresolved_function_call" {
				t.Errorf("%s fabricated a nil callable alternative for a map receiver", test.name)
			}
		}
		if len(acquired) == 0 || !released {
			t.Errorf("%s lost configured bound-map acquire/release identity", test.name)
		}
		if uncertain != test.wantUncertain {
			t.Errorf("%s unresolved bound-map receiver=%t, want %t", test.name, uncertain, test.wantUncertain)
		}
	}
}

func knownTaskBoundaryState(t *testing.T, report Report, root string) {
	t.Helper()
	for _, test := range []struct {
		name          string
		wantUncertain bool
	}{
		{"QueryTaskMapNilPhi", true},
		{"QueryTaskMapKnownOnly", false},
	} {
		line := sourceLineInFunction(t, root, "app.go", test.name, "group.Go(tasks[\"known\"])")
		var operation string
		for _, edge := range report.Relationships {
			if edge.Kind == "waitgroup_go" && strings.HasSuffix(edge.From, "::"+test.name) && strings.Contains(edge.Evidence.Snippet, "group.Go(tasks[\"known\"])") {
				if edge.Evidence.File != "app.go" || edge.Evidence.Line != line {
					t.Fatalf("%s WaitGroup.Go edge has wrong source evidence (want app.go:%d): %+v", test.name, line, edge.Evidence)
				}
				operation = edge.To
			}
		}
		if operation == "" {
			t.Fatalf("%s missing source-backed WaitGroup.Go edge", test.name)
		}
		known := false
		uncertain := false
		for _, edge := range report.Relationships {
			if edge.Kind == "waitgroup_task" && edge.From == operation && strings.HasSuffix(edge.To, "::knownTask") {
				known = true
			}
		}
		for _, boundary := range report.Boundaries {
			if boundary.Kind == "unresolved_waitgroup_task" && strings.HasSuffix(boundary.Node, "::"+test.name) && boundary.Evidence.File == "app.go" && boundary.Evidence.Line == line && strings.Contains(boundary.Evidence.Snippet, "group.Go(tasks[\"known\"])") {
				uncertain = true
			}
		}
		if !known {
			t.Errorf("%s lost its knownTask alternative", test.name)
		}
		if uncertain != test.wantUncertain {
			t.Errorf("%s unresolved WaitGroup task=%t, want %t", test.name, uncertain, test.wantUncertain)
		}
	}
}

func nestedTaskMapBoundaryState(t *testing.T, report Report, root string) {
	t.Helper()
	for _, test := range []struct {
		name    string
		snippet string
		wantNil bool
	}{
		{"QueryNestedTaskMapPhi", `group.Go(tasks["known"]["task"])`, true},
		{"QueryNestedTaskMapKnownOnly", `group.Go(tasks["known"]["task"])`, false},
		{"QueryArrayTaskMapNilPhi", `group.Go(tasks["known"][0]["task"])`, true},
		{"QueryArrayTaskMapKnownOnly", `group.Go(tasks["known"][0]["task"])`, false},
	} {
		line := sourceLineInFunction(t, root, "app.go", test.name, test.snippet)
		var operation string
		for _, edge := range report.Relationships {
			if edge.Kind == "waitgroup_go" && edge.From == "example.com/maplookup::"+test.name && edge.Evidence.File == "app.go" && edge.Evidence.Line == line && strings.Contains(edge.Evidence.Snippet, test.snippet) {
				operation = edge.To
			}
		}
		if operation == "" {
			t.Fatalf("%s missing source-backed WaitGroup.Go edge at app.go:%d", test.name, line)
		}
		known, nilAlternative := false, false
		for _, edge := range report.Relationships {
			if edge.Kind == "waitgroup_task" && edge.From == operation && strings.HasSuffix(edge.To, "::knownTask") && edge.Evidence.File == "app.go" && edge.Evidence.Line == line && strings.Contains(edge.Evidence.Snippet, test.snippet) {
				known = true
			}
		}
		for _, boundary := range report.Boundaries {
			nilReason := strings.Contains(boundary.Reason, "typed nil function value") || strings.Contains(boundary.Reason, "nil callback alternative") || strings.Contains(boundary.Reason, "possible nil")
			if boundary.Kind == "unresolved_waitgroup_task" && boundary.Node == "example.com/maplookup::"+test.name && boundary.Evidence.File == "app.go" && boundary.Evidence.Line == line && strings.Contains(boundary.Evidence.Snippet, test.snippet) && nilReason {
				nilAlternative = true
			}
		}
		if !known {
			t.Errorf("%s lost the knownTask alternative", test.name)
		}
		if nilAlternative != test.wantNil {
			t.Errorf("%s typed-nil map alternative=%t, want %t", test.name, nilAlternative, test.wantNil)
		}
	}
}

func pointerMapChildState(t *testing.T, report Report, root string) {
	t.Helper()
	owner := "example.com/maplookup::QueryPointerMapChildAlternative"
	snippet := `group.Go((*child)["known"])`
	line := sourceLineInFunction(t, root, "app.go", "QueryPointerMapChildAlternative", snippet)
	var operation string
	for _, edge := range report.Relationships {
		if edge.Kind == "waitgroup_go" && edge.From == owner && edge.Evidence.File == "app.go" && edge.Evidence.Line == line && strings.Contains(edge.Evidence.Snippet, snippet) {
			operation = edge.To
		}
	}
	if operation == "" {
		t.Fatalf("pointer-map child control lost its source-backed Go site at app.go:%d", line)
	}
	known := false
	for _, edge := range report.Relationships {
		known = known || edge.Kind == "waitgroup_task" && edge.From == operation && strings.HasSuffix(edge.To, "::knownTask")
	}
	if !known {
		t.Errorf("pointer-map child control lost the valid known callback candidate")
	}
	for _, boundary := range report.Boundaries {
		if boundary.Kind == "unresolved_waitgroup_task" && boundary.Node == owner && boundary.Evidence.File == "app.go" && boundary.Evidence.Line == line && strings.Contains(boundary.Evidence.Snippet, snippet) {
			t.Errorf("nil pointer to a child map fabricated an invocation alternative before dereference: %+v", boundary)
		}
	}
}

func sourceLineInFunction(t *testing.T, root, name, function, snippet string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	inFunction := false
	for lineNumber, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "func ") {
			inFunction = strings.HasPrefix(line, "func "+function+"(")
		}
		if inFunction && strings.Contains(line, snippet) {
			return lineNumber + 1
		}
	}
	return 0
}
