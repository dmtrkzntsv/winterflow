package template

import (
	"strconv"

	"winterflow/internal/domain/model"
)

// expand substitutes ${NAME} with the plain values (validation allows no
// other reference in the fields it is used on).
func expand(s string, plain map[string]string) string {
	return refRE.ReplaceAllStringFunc(s, func(m string) string {
		return plain[m[2:len(m)-1]]
	})
}

// renderIngress renders the rows. A port from a parameter must be a port
// number: the error names the parameter (at save, example values stand in
// for the real ones, so only host syntax is meant there: lenient skips the
// port check).
func renderIngress(rows []IngressRow, plain map[string]string, lenient bool) ([]model.IngressDomain, error) {
	out := []model.IngressDomain{}
	for _, row := range rows {
		port, err := strconv.Atoi(expand(string(row.Port), plain))
		if err != nil || port < 1 || port > 65535 {
			if !lenient {
				m := refRE.FindStringSubmatch(string(row.Port))
				name := string(row.Port)
				if m != nil {
					name = m[1]
				}
				return nil, invalid("parameters."+name, "%q is not a port 1-65535", expand(string(row.Port), plain))
			}
			port = 1
		}
		out = append(out, model.IngressDomain{Domain: expand(row.Host, plain), UpstreamPort: port})
	}
	return out, nil
}
