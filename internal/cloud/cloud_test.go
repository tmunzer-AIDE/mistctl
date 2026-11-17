package cloud

import "testing"

func TestLookup(t *testing.T) {
	for in, want := range map[string]string{
		"global01":                     "api.mist.com",
		"Global-01":                    "api.mist.com",
		"api.eu.mist.com":              "api.eu.mist.com",
		"https://manage.ac5.mist.com/": "api.ac5.mist.com",
		"Mist EMEA 03":                 "api.ac6.mist.com",
		"gov":                          "api.us.mist-federal.com",
		"api.us.mist-federal.com":      "api.us.mist-federal.com",
	} {
		c, err := Lookup(in)
		if err != nil || c.Host != want {
			t.Errorf("Lookup(%q) = %v, %v; want %s", in, c.Host, err, want)
		}
	}
	if _, err := Lookup("mars"); err == nil {
		t.Error("expected error")
	}
}
