package template

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"winterflow/internal/domain/model"
)

// portRE is the one spelling of a port number: no sign, no leading zero.
var portRE = regexp.MustCompile(`^[1-9][0-9]{0,4}$`)

// canonicalPort parses s as a port 1-65535 in its canonical spelling.
func canonicalPort(s string) (int, bool) {
	if !portRE.MatchString(s) {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n <= 65535
}

// expand substitutes ${NAME} with the plain values (validation allows no
// other reference in the fields it is used on).
func expand(s string, plain map[string]string) string {
	return refRE.ReplaceAllStringFunc(s, func(m string) string {
		return plain[m[2:len(m)-1]]
	})
}

// refNames are the parameter names s references, in order.
func refNames(s string) []string {
	var out []string
	for _, m := range refRE.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// valueField names where a bad rendered value came from: the parameters
// the template string used, or fallback when it used none.
func valueField(s, fallback string) string {
	if names := refNames(s); len(names) > 0 {
		return "parameters." + strings.Join(names, ",")
	}
	return fallback
}

// renderIngress renders the rows and validates the result with the
// ingress rules (hostname syntax, duplicates, port range), so a parameter
// value can't put an arbitrary or malformed host into the config. The error
// names the parameters the failing field uses. A port from a parameter must
// be a port number. At save, example values stand in for the real ones, so
// only host syntax is meant there: lenient skips the port check.
func renderIngress(rows []IngressRow, plain map[string]string, lenient bool) ([]model.IngressDomain, error) {
	out := []model.IngressDomain{}
	for i, row := range rows {
		port, ok := canonicalPort(expand(string(row.Port), plain))
		if !ok {
			if !lenient {
				return nil, Invalid(valueField(string(row.Port), fmt.Sprintf("ingress[%d].port", i)),
					"%q is not a port 1-65535", expand(string(row.Port), plain))
			}
			port = 1
		}
		d := model.IngressDomain{Domain: expand(row.Host, plain), UpstreamPort: port}
		if err := (&model.Ingress{Domains: []model.IngressDomain{d}}).Validate(); err != nil {
			return nil, Invalid(valueField(row.Host, fmt.Sprintf("ingress[%d].host", i)), "%v", err)
		}
		out = append(out, d)
	}
	if err := (&model.Ingress{Domains: out}).Validate(); err != nil {
		return nil, Invalid("ingress", "%v", err)
	}
	return out, nil
}
