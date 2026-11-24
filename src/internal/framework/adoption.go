package framework

import (
	"encoding/json"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

func (s WriteSpec) adoptionMatch() func(details map[string]any) bool {
	if s.op() != WriteOpCreate {
		return s.Match
	}
	want := bodyFields(s.Body)
	return func(details map[string]any) bool {
		return s.Match(details) && payloadAgrees(details, want)
	}
}

func bodyFields(body any) map[string]any {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil
	}
	fields := map[string]any{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil
	}
	return fields
}

// payloadAgrees reports a mismatch only where both sides hold comparable values that differ after the backend's normalisation.
func payloadAgrees(details, want map[string]any) bool {
	for key, value := range want {
		if got, present := details[key]; present && !valuesAgree(got, value) {
			return false
		}
	}
	return true
}

func valuesAgree(got, want any) bool {
	if got == nil || want == nil {
		return true
	}
	gotMap, gotIsMap := got.(map[string]any)
	wantMap, wantIsMap := want.(map[string]any)
	if gotIsMap && wantIsMap {
		return payloadAgrees(gotMap, wantMap)
	}
	return listsAgree(asList(got), asList(want))
}

func asList(value any) []any {
	if list, ok := value.([]any); ok {
		return list
	}
	return []any{value}
}

func listsAgree(got, want []any) bool {
	gotTexts, gotLeaves := leafTexts(got)
	wantTexts, wantLeaves := leafTexts(want)
	if gotLeaves != wantLeaves {
		return true
	}
	if len(got) != len(want) {
		return false
	}
	if gotLeaves {
		slices.Sort(gotTexts)
		slices.Sort(wantTexts)
		return slices.Equal(gotTexts, wantTexts)
	}
	for i := range want {
		if !valuesAgree(got[i], want[i]) {
			return false
		}
	}
	return true
}

func leafTexts(values []any) ([]string, bool) {
	texts := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := leafText(value)
		if !ok {
			return nil, false
		}
		texts = append(texts, text)
	}
	return texts, true
}

func leafText(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return canonicalText(v), true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(v), true
	}
	return "", false
}

func canonicalText(s string) string {
	text := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
	if addr, err := netip.ParseAddr(text); err == nil {
		return addr.String()
	}
	if prefix, err := netip.ParsePrefix(text); err == nil {
		return prefix.String()
	}
	if pairs, ok := hexPairs(text); ok {
		return pairs
	}
	return text
}

func hexPairs(text string) (string, bool) {
	parts := strings.FieldsFunc(text, func(r rune) bool { return r == ':' || r == '-' })
	if len(parts) < 2 || strings.Count(text, "-")+strings.Count(text, ":") != len(parts)-1 {
		return "", false
	}
	for _, part := range parts {
		if len(part) != 2 || !isHex(part) {
			return "", false
		}
	}
	return strings.Join(parts, ":"), true
}

func isHex(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}
