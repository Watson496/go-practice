package httpboundary

import "testing"

func Test配信ルートの外を指すURLパスを拒否する(t *testing.T) {
	for _, test := range []struct {
		path  string
		valid bool
	}{
		{path: "/", valid: true},
		{path: "/api/games", valid: true},
		{path: "/assets/app.js", valid: true},
		{path: "/../outside.txt", valid: false},
		{path: `/..\outside.txt`, valid: false},
		{path: "/./assets/app.js", valid: false},
	} {
		if got := ValidURLPath(test.path); got != test.valid {
			t.Errorf("ValidURLPath(%q) = %t、期待値 = %t", test.path, got, test.valid)
		}
	}
}
