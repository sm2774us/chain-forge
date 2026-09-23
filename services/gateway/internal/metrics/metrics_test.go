package metrics

import (
	"strings"
	"testing"
)

func TestRegistry(t *testing.T) {
	r := New()
	r.Describe("req_total", "requests")
	r.Inc("req_total", map[string]string{"b": "2", "a": "1"})
	r.Add("req_total", map[string]string{"b": "2", "a": "1"}, 2)
	r.Add("req_total", map[string]string{"a": "x"}, 1)
	r.Set("up", nil, 1)
	if v := r.Value("req_total", map[string]string{"a": "1", "b": "2"}); v != 3 {
		t.Fatalf("got %v", v)
	}
	var sb strings.Builder
	r.Write(&sb)
	out := sb.String()
	if strings.Count(out, "# HELP req_total") != 1 || !strings.Contains(out, `req_total{a="1",b="2"} 3`) || !strings.Contains(out, "up 1") {
		t.Fatalf("bad exposition:\n%s", out)
	}
}
