// SPDX-License-Identifier: AGPL-3.0-or-later

package cores

import (
	"bytes"
	"encoding/json"
	"sort"
)

// jsonMarshalOrdered serialises a Capabilities struct with the
// flags sorted alphabetically and the named fields in a
// fixed order. The format is:
//
//	{"name":"…","version":"…","capabilities":["A","B","C"]}
//
// We do this by hand instead of using a regular struct tag
// because we want the capabilities slice to be sorted
// regardless of provider-side order — that way the panel
// diff between two capability sets is meaningful.
//
// Exported via Capabilities.MarshalJSON above; lives in its
// own file because the sort helper is reused by tests.
func jsonMarshalOrdered(capabilities []string, name, version string) ([]byte, error) {
	sort.Strings(capabilities)
	var buf bytes.Buffer
	buf.WriteString(`{"name":`)
	if name == "" {
		buf.WriteString(`""`)
	} else {
		b, err := json.Marshal(name)
		if err != nil {
			return nil, err
		}
		buf.Write(b)
	}
	buf.WriteString(`,"version":`)
	if version == "" {
		buf.WriteString(`""`)
	} else {
		b, err := json.Marshal(version)
		if err != nil {
			return nil, err
		}
		buf.Write(b)
	}
	buf.WriteString(`,"capabilities":[`)
	for i, c := range capabilities {
		if i > 0 {
			buf.WriteByte(',')
		}
		b, err := json.Marshal(c)
		if err != nil {
			return nil, err
		}
		buf.Write(b)
	}
	buf.WriteString(`]}`)
	return buf.Bytes(), nil
}
