package issues

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// KV and Obj give an ordered JSON object, so output matches Python's dict order.
type KV struct {
	K string
	V any
}
type Obj []KV

// dumps renders v the way Python's json.dumps does (", " and ": " separators).
// ascii=false matches ensure_ascii=False.
func dumps(v any, ascii bool) string {
	var b strings.Builder
	enc(&b, v, ascii)
	return b.String()
}

func enc(b *strings.Builder, v any, ascii bool) {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case string:
		encStr(b, x, ascii)
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case int:
		b.WriteString(strconv.Itoa(x))
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case float64:
		if x == float64(int64(x)) && x < 1e15 && x > -1e15 {
			b.WriteString(strconv.FormatFloat(x, 'f', 1, 64))
		} else {
			b.WriteString(strconv.FormatFloat(x, 'f', -1, 64))
		}
	case []string:
		b.WriteByte('[')
		for i, s := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			encStr(b, s, ascii)
		}
		b.WriteByte(']')
	case []int:
		b.WriteByte('[')
		for i, s := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(strconv.Itoa(s))
		}
		b.WriteByte(']')
	case []any:
		b.WriteByte('[')
		for i, s := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			enc(b, s, ascii)
		}
		b.WriteByte(']')
	case Obj:
		b.WriteByte('{')
		for i, kv := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			encStr(b, kv.K, ascii)
			b.WriteString(": ")
			enc(b, kv.V, ascii)
		}
		b.WriteByte('}')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		o := Obj{}
		for _, k := range keys {
			o = append(o, KV{k, x[k]})
		}
		enc(b, o, ascii)
	default:
		panic(fmt.Sprintf("dumps: unsupported %T", v))
	}
}

func encStr(b *strings.Builder, s string, ascii bool) {
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r < 0x20:
			fmt.Fprintf(b, `\u%04x`, r)
		case r > 0x7e && ascii && r != 0x7f:
			if r > 0xffff {
				r -= 0x10000
				fmt.Fprintf(b, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
			} else {
				fmt.Fprintf(b, `\u%04x`, r)
			}
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}
