package vars

import "winterflow/internal/domain/varref"

// References adds to into every variable name data references (see
// varref.References).
func References(data []byte, into map[string]bool) { varref.References(data, into) }
