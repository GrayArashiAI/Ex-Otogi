package settingstest

import "testing"

type inner struct {
	Limit int `json:"limit,omitempty" default:"8"`
}

type outer struct {
	Items []inner `json:"items,omitempty"`
	Extra *inner  `json:"extra,omitempty"`
	Unset *inner  `json:"unset,omitempty"`
}

// The copy gets the defaults, in slice elements and behind set pointers,
// and the settings passed in keep their unset fields: a test compares the
// two.
func TestWithDefaultsLeavesItsArgumentAlone(t *testing.T) {
	t.Parallel()
	unset := outer{Items: []inner{{}}, Extra: &inner{}}
	typed := WithDefaults(t, unset)
	if typed.Items[0].Limit != 8 || typed.Extra.Limit != 8 || typed.Unset != nil {
		t.Fatalf("typed %+v, extra %+v", typed, typed.Extra)
	}
	if unset.Items[0].Limit != 0 || unset.Extra.Limit != 0 {
		t.Fatalf("the settings passed in changed: %+v, extra %+v", unset, unset.Extra)
	}
}
