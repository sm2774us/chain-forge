// Package metrics renders a tiny Prometheus text-format registry (counters and
// gauges) without third-party dependencies.
package metrics

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

// Registry holds counters keyed by name+labels.
type Registry struct {
	mu   sync.Mutex
	vals map[string]float64
	help map[string]string
}

// New creates a Registry.
func New() *Registry {
	return &Registry{vals: map[string]float64{}, help: map[string]string{}}
}

func key(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return name
	}
	ks := make([]string, 0, len(labels))
	for k := range labels {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	parts := make([]string, len(ks))
	for i, k := range ks {
		parts[i] = fmt.Sprintf("%s=%q", k, labels[k])
	}
	return name + "{" + strings.Join(parts, ",") + "}"
}

// Describe sets HELP text for a metric family.
func (r *Registry) Describe(name, help string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.help[name] = help
}

// Inc adds 1 to a counter.
func (r *Registry) Inc(name string, labels map[string]string) { r.Add(name, labels, 1) }

// Add adds v to a counter.
func (r *Registry) Add(name string, labels map[string]string, v float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.vals[key(name, labels)] += v
}

// Set assigns a gauge value.
func (r *Registry) Set(name string, labels map[string]string, v float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.vals[key(name, labels)] = v
}

// Value reads a series (used by tests and the dashboard summary).
func (r *Registry) Value(name string, labels map[string]string) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.vals[key(name, labels)]
}

// Write emits the exposition format in stable order.
func (r *Registry) Write(w io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	keys := make([]string, 0, len(r.vals))
	for k := range r.vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	seen := map[string]bool{}
	for _, k := range keys {
		fam := k
		if i := strings.IndexByte(k, '{'); i >= 0 {
			fam = k[:i]
		}
		if h, ok := r.help[fam]; ok && !seen[fam] {
			fmt.Fprintf(w, "# HELP %s %s\n", fam, h)
			seen[fam] = true
		}
		fmt.Fprintf(w, "%s %g\n", k, r.vals[k])
	}
}
