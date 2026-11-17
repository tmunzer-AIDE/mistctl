// Package cloud maps user-friendly Mist cloud names and API hosts to the
// environments known by mistapi-go.
package cloud

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tmunzer/mistapi-go/mistapi"
)

// Cloud describes one Mist cloud instance.
type Cloud struct {
	Name string              // short name used on the command line, e.g. "global01"
	Host string              // API host, e.g. "api.mist.com"
	Env  mistapi.Environment // mistapi-go environment
}

// All lists every cloud supported by the underlying SDK.
var All = []Cloud{
	{"global01", "api.mist.com", mistapi.MIST_GLOBAL_01},
	{"global02", "api.gc1.mist.com", mistapi.MIST_GLOBAL_02},
	{"global03", "api.ac2.mist.com", mistapi.MIST_GLOBAL_03},
	{"global04", "api.gc2.mist.com", mistapi.MIST_GLOBAL_04},
	{"global05", "api.gc4.mist.com", mistapi.MIST_GLOBAL_05},
	{"emea01", "api.eu.mist.com", mistapi.MIST_EMEA_01},
	{"emea02", "api.gc3.mist.com", mistapi.MIST_EMEA_02},
	{"emea03", "api.ac6.mist.com", mistapi.MIST_EMEA_03},
	{"emea04", "api.gc6.mist.com", mistapi.MIST_EMEA_04},
	{"apac01", "api.ac5.mist.com", mistapi.MIST_APAC_01},
	{"apac02", "api.gc5.mist.com", mistapi.MIST_APAC_02},
	{"apac03", "api.gc7.mist.com", mistapi.MIST_APAC_03},
	{"gov", "api.us.mist-federal.com", mistapi.GOV_CLOUD},
	{"staging", "api.mistsys.com", mistapi.AWS_STAGING},
}

// Lookup resolves a cloud by short name ("global01"), API host
// ("api.mist.com"), portal host ("manage.mist.com") or SDK environment name
// ("Mist Global 01"). Matching is case-insensitive.
func Lookup(s string) (Cloud, error) {
	q := strings.ToLower(strings.TrimSpace(s))
	q = strings.TrimPrefix(q, "https://")
	q = strings.TrimSuffix(q, "/")
	q = strings.ReplaceAll(q, "-", "")
	q = strings.ReplaceAll(q, " ", "")
	if q == "" {
		return Cloud{}, fmt.Errorf("empty cloud name")
	}
	for _, c := range All {
		host := strings.ReplaceAll(c.Host, "-", "")
		portal := strings.Replace(host, "api.", "manage.", 1)
		env := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(string(c.Env), " ", ""), "-", ""))
		if q == c.Name || q == host || q == portal || q == env {
			return c, nil
		}
	}
	// Convenience aliases.
	switch q {
	case "global", "us":
		return All[0], nil
	case "emea", "eu":
		return Lookup("emea01")
	case "apac":
		return Lookup("apac01")
	case "govcloud", "federal":
		return Lookup("gov")
	}
	return Cloud{}, fmt.Errorf("unknown Mist cloud %q (valid: %s)", s, strings.Join(Names(), ", "))
}

// Names returns the short names of all clouds, sorted.
func Names() []string {
	out := make([]string, 0, len(All))
	for _, c := range All {
		out = append(out, c.Name)
	}
	sort.Strings(out)
	return out
}
