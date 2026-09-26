// Package scenarios defines the named fault-injection behaviours. Each fault
// changes one aspect of the normal response planned by package rangeserver.
package scenarios

import (
	"fmt"
	"net/http"

	"github.com/lansenou/pmtiles-conformance-lab/internal/rangeserver"
)

// HTTP validity labels (docs/plan.md §10).
const (
	Valid         = "valid"
	ValidUnusual  = "valid-but-unusual"
	Invalid       = "invalid"
	ValidBlocksJS = "valid-http-blocks-browser-read"
)

// All returns every scenario in documentation order. Names are stable.
func All() []rangeserver.Scenario {
	return []rangeserver.Scenario{
		{Name: "normal", Validity: Valid, Description: "Correct RFC 9110 single-range responses."},
		{
			Name: "wrong-content-range", Validity: Invalid,
			Description: "206 with the correct body but Content-Range shifted by +1 byte.",
			Mutate: func(x *rangeserver.Exchange) {
				if x.Resp.Status != http.StatusPartialContent {
					return
				}
				var a, b, size int64
				fmt.Sscanf(x.Resp.Header.Get("Content-Range"), "bytes %d-%d/%d", &a, &b, &size)
				x.Resp.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", a+1, b+1, size))
			},
		},
	}
}

// Lookup returns the scenario with the given name.
func Lookup(name string) (rangeserver.Scenario, bool) {
	for _, s := range All() {
		if s.Name == name {
			return s, true
		}
	}
	return rangeserver.Scenario{}, false
}
