// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package metricdef_test

import (
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/observability/metricdef"
)

var (
	jobsTotal = metricdef.Def{
		Name: "app_jobs_total", Help: "Jobs finished.", Kind: metricdef.KindCounter,
		Labels: []string{"tenant", "outcome"},
		Limits: map[string]metricdef.LabelLimit{
			"outcome": {Allow: []string{"ok", "failed"}},
			"tenant":  {MaxDistinct: 2},
		},
	}
	queueDepth = metricdef.Def{Name: "app_queue_depth", Help: "Queued jobs.", Kind: metricdef.KindGauge, Labels: []string{"queue"}}
	jobSeconds = metricdef.Def{
		Name: "app_job_duration_seconds", Help: "Job latency.", Unit: "seconds", Kind: metricdef.KindHistogram,
		Labels: []string{"backend"}, Buckets: []float64{0.1, 1, 10},
	}
)

func TestDefValidateAcceptsWellFormedDefs(t *testing.T) {
	t.Parallel()
	for _, d := range []metricdef.Def{
		jobsTotal, queueDepth, jobSeconds,
		{Name: "app_bytes_sent_bytes_total", Help: "x", Unit: "bytes", Kind: metricdef.KindCounter},
		{Name: "app_build_info", Help: "x", Kind: metricdef.KindGauge, Labels: []string{"version"}},
	} {
		if err := d.Validate(); err != nil {
			t.Errorf("%s: %v", d.Name, err)
		}
	}
}

func TestDefValidateRejectsMalformedDefs(t *testing.T) {
	t.Parallel()
	gauge := func(mut func(*metricdef.Def)) metricdef.Def {
		d := metricdef.Def{Name: "app_x", Help: "x", Kind: metricdef.KindGauge, Labels: []string{"a"}}
		mut(&d)
		return d
	}
	hist := func(d *metricdef.Def) { d.Kind = metricdef.KindHistogram }
	cases := []struct {
		want string
		def  metricdef.Def
	}{
		{"invalid metric name", gauge(func(d *metricdef.Def) { d.Name = "app-x" })},
		{"invalid metric name", gauge(func(d *metricdef.Def) { d.Name = "app:x" })},
		{"invalid metric name", gauge(func(d *metricdef.Def) { d.Name = "__app_x" })},
		{"help is required", gauge(func(d *metricdef.Def) { d.Help = "" })},
		{"counter names end", gauge(func(d *metricdef.Def) { d.Kind = metricdef.KindCounter })},
		{"only counters end", gauge(func(d *metricdef.Def) { d.Name = "app_x_total" })},
		{"unknown kind", gauge(func(d *metricdef.Def) { d.Kind = 0 })},
		{"reserved suffix _count", gauge(func(d *metricdef.Def) { d.Name = "app_x_count" })},
		{"name must end in _seconds", gauge(func(d *metricdef.Def) { d.Unit = "seconds" })},
		{"not a base unit", gauge(func(d *metricdef.Def) { d.Name, d.Unit = "app_x_milliseconds", "milliseconds" })},
		{"invalid unit", gauge(func(d *metricdef.Def) { d.Name, d.Unit = "app_x_Sec", "Sec" })},
		{"invalid label", gauge(func(d *metricdef.Def) { d.Labels = []string{"a-b"} })},
		{"invalid label", gauge(func(d *metricdef.Def) { d.Labels = []string{"__a"} })},
		{"duplicate label", gauge(func(d *metricdef.Def) { d.Labels = []string{"a", "a"} })},
		{`"quantile" is reserved`, gauge(func(d *metricdef.Def) { d.Labels = []string{"quantile"} })},
		{`"le" is reserved`, gauge(func(d *metricdef.Def) { hist(d); d.Labels = []string{"le"} })},
		{"only valid on histograms", gauge(func(d *metricdef.Def) { d.Buckets = []float64{1} })},
		{"increase strictly", gauge(func(d *metricdef.Def) { hist(d); d.Buckets = []float64{1, 1} })},
		{"bucket 0 is NaN", gauge(func(d *metricdef.Def) { hist(d); d.Buckets = []float64{math.NaN()} })},
		{"undeclared label", gauge(func(d *metricdef.Def) { d.Limits = map[string]metricdef.LabelLimit{"b": {MaxDistinct: 1}} })},
		{"exactly one", gauge(func(d *metricdef.Def) {
			d.Limits = map[string]metricdef.LabelLimit{"a": {Allow: []string{"x"}, MaxDistinct: 1}}
		})},
		{"exactly one", gauge(func(d *metricdef.Def) { d.Limits = map[string]metricdef.LabelLimit{"a": {}} })},
		{"exactly one", gauge(func(d *metricdef.Def) { d.Limits = map[string]metricdef.LabelLimit{"a": {MaxDistinct: -1}} })},
	}
	for _, tc := range cases {
		err := tc.def.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err = %v, want it to mention %q", tc.def.Name, err, tc.want)
		}
	}
}

func TestDefSeriesAndLabels(t *testing.T) {
	t.Parallel()
	if got := jobSeconds.Series(); !slices.Equal(got, []string{"app_job_duration_seconds_bucket", "app_job_duration_seconds_count", "app_job_duration_seconds_sum"}) {
		t.Errorf("histogram series = %v", got)
	}
	if got := jobsTotal.Series(); !slices.Equal(got, []string{"app_jobs_total"}) {
		t.Errorf("counter series = %v", got)
	}
	if got := jobSeconds.SeriesLabels("app_job_duration_seconds_bucket"); !slices.Equal(got, []string{"backend", "le"}) {
		t.Errorf("bucket labels = %v", got)
	}
	if got := jobSeconds.SeriesLabels("app_job_duration_seconds_count"); !slices.Equal(got, []string{"backend"}) {
		t.Errorf("count labels = %v", got)
	}
	if metricdef.KindHistogram.String() != "histogram" || metricdef.Kind(9).String() != "Kind(9)" {
		t.Error("Kind.String mismatch")
	}
}

func TestCatalogLookupsAndCopies(t *testing.T) {
	t.Parallel()
	cat, err := metricdef.NewCatalog(queueDepth, jobSeconds, jobsTotal)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	defs := cat.Defs()
	if names := []string{defs[0].Name, defs[1].Name, defs[2].Name}; !slices.IsSorted(names) {
		t.Errorf("Defs not sorted: %v", names)
	}
	defs[0].Labels[0] = "mutated"
	if d, _ := cat.Lookup(defs[0].Name); d.Labels[0] == "mutated" {
		t.Error("Defs leaked internal label slice")
	}
	if d, ok := cat.LookupSeries("app_job_duration_seconds_bucket"); !ok || d.Name != jobSeconds.Name {
		t.Errorf("LookupSeries(bucket) = %v, %t", d.Name, ok)
	}
	if _, ok := cat.LookupSeries("app_job_duration_seconds"); ok {
		t.Error("histogram base name is not an exposed series")
	}
	if _, ok := cat.Lookup("missing"); ok {
		t.Error("Lookup(missing) succeeded")
	}
	if got := len(cat.Series()); got != 5 {
		t.Errorf("Series() has %d names, want 5", got)
	}
	if !strings.Contains(cat.String(), "app_queue_depth") {
		t.Errorf("String() = %s", cat)
	}
}

func TestCatalogRejectsDuplicatesAndFamilyClashes(t *testing.T) {
	t.Parallel()
	if _, err := metricdef.NewCatalog(queueDepth, queueDepth); err == nil {
		t.Error("duplicate name accepted")
	}
	clash := metricdef.Def{Name: "app_jobs", Help: "x", Kind: metricdef.KindGauge}
	if _, err := metricdef.NewCatalog(jobsTotal, clash); err == nil || !strings.Contains(err.Error(), "family") {
		t.Errorf("OpenMetrics family clash: err = %v", err)
	}
	if _, err := metricdef.NewCatalog(metricdef.Def{Name: "bad-name"}); err == nil {
		t.Error("invalid def accepted")
	}
	empty, err := metricdef.NewCatalog()
	if err != nil || len(empty.Defs()) != 0 {
		t.Errorf("empty catalog: %v %v", empty, err)
	}
}

func TestCatalogMerge(t *testing.T) {
	t.Parallel()
	a, errA := metricdef.NewCatalog(queueDepth)
	b, errB := metricdef.NewCatalog(jobSeconds)
	if errA != nil || errB != nil {
		t.Fatal(errA, errB)
	}
	merged, err := a.Merge(b)
	if err != nil || len(merged.Defs()) != 2 {
		t.Fatalf("Merge = %v, %v", merged, err)
	}
	if _, err := merged.Merge(a); err == nil {
		t.Error("Merge accepted a duplicate def")
	}
}
