package contract

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func (v *schemaValidator) checkString(s map[string]any, instance any, instPtr, schemaPath string) error {
	str, ok := instance.(string)
	if !ok {
		return nil
	}
	if m, ok := lookupInt(s, "maxLength"); ok && utf8.RuneCountInString(str) > int(m) {
		return v.fail("maxLength", schemaPath, instPtr, "string is longer than %d characters", m)
	}
	if m, ok := lookupInt(s, "minLength"); ok && utf8.RuneCountInString(str) < int(m) {
		return v.fail("minLength", schemaPath, instPtr, "string is shorter than %d characters", m)
	}
	if p, ok := s["pattern"].(string); ok {
		re, err := regexp.Compile(p)
		if err != nil {
			return &SchemaError{
				Keyword: "pattern", SchemaPath: schemaPath,
				Message: fmt.Sprintf("pattern %q does not compile", p),
				Err:     ErrSchemaMalformed,
			}
		}
		if !re.MatchString(str) {
			return v.fail("pattern", schemaPath, instPtr, "string does not match pattern %q", p)
		}
	}
	if f, ok := s["format"].(string); ok {
		if check, known := formatChecks[f]; known && !check(str) {
			return v.fail("format", schemaPath, instPtr, "string does not satisfy format %q", f)
		}
	}
	return nil
}

// lookupInt reads a nonnegative integer-valued schema keyword.
func lookupInt(s map[string]any, key string) (int64, bool) {
	raw, ok := s[key].(json.Number)
	if !ok || !integerLiteral(raw) {
		return 0, false
	}
	val, err := parseInteger(raw)
	if err != nil || val < 0 {
		return 0, false
	}
	return val, true
}

func (v *schemaValidator) checkArray(s map[string]any, instance any, instPtr, schemaPath string, depth int) error {
	arr, ok := instance.([]any)
	if !ok {
		return nil
	}
	if m, ok := lookupInt(s, "maxItems"); ok && int64(len(arr)) > m {
		return v.fail("maxItems", schemaPath, instPtr, "array has %d items, limit %d", len(arr), m)
	}
	if m, ok := lookupInt(s, "minItems"); ok && int64(len(arr)) < m {
		return v.fail("minItems", schemaPath, instPtr, "array has %d items, minimum %d", len(arr), m)
	}
	if u, ok := s["uniqueItems"].(bool); ok && u {
		for i := 0; i < len(arr); i++ {
			for j := i + 1; j < len(arr); j++ {
				if deepEqualJSON(arr[i], arr[j]) {
					return v.fail("uniqueItems", schemaPath, instPtr, "array items %d and %d are equal", i, j)
				}
			}
		}
	}
	prefix, hasPrefix := s["prefixItems"].([]any)
	if hasPrefix {
		for i, sub := range prefix {
			if i >= len(arr) {
				break
			}
			if err := v.validate(arr[i], sub, instPtr+"/"+strconv.Itoa(i), schemaPath+"/prefixItems/"+strconv.Itoa(i), depth+1); err != nil {
				return err
			}
		}
	}
	if items, ok := s["items"]; ok {
		for i := len(prefix); i < len(arr); i++ {
			if err := v.validate(arr[i], items, instPtr+"/"+strconv.Itoa(i), schemaPath+"/items", depth+1); err != nil {
				return err
			}
		}
	}
	if contains, hasContains := s["contains"]; hasContains {
		minCount := int64(1)
		if m, ok := lookupInt(s, "minContains"); ok {
			minCount = m
		}
		maxCount := int64(len(arr))
		if m, ok := lookupInt(s, "maxContains"); ok {
			maxCount = m
		}
		count := int64(0)
		for i, el := range arr {
			if v.validate(el, contains, instPtr+"/"+strconv.Itoa(i), schemaPath+"/contains", depth+1) == nil {
				count++
			}
		}
		if count < minCount {
			return v.fail("contains", schemaPath, instPtr, "array has %d matching items, minimum %d", count, minCount)
		}
		if count > maxCount {
			return v.fail("maxContains", schemaPath, instPtr, "array has %d matching items, maximum %d", count, maxCount)
		}
	}
	return nil
}

func (v *schemaValidator) checkObject(s map[string]any, instance any, instPtr, schemaPath string, depth int) error {
	obj, ok := instance.(map[string]any)
	if !ok {
		return nil
	}
	if m, ok := lookupInt(s, "maxProperties"); ok && int64(len(obj)) > m {
		return v.fail("maxProperties", schemaPath, instPtr, "object has %d properties, limit %d", len(obj), m)
	}
	if m, ok := lookupInt(s, "minProperties"); ok && int64(len(obj)) < m {
		return v.fail("minProperties", schemaPath, instPtr, "object has %d properties, minimum %d", len(obj), m)
	}
	if req, ok := s["required"].([]any); ok {
		for _, r := range req {
			name, isStr := r.(string)
			if !isStr {
				return &SchemaError{
					Keyword: "required", SchemaPath: schemaPath,
					Message: "required entries must be strings", Err: ErrSchemaMalformed,
				}
			}
			if _, present := obj[name]; !present {
				return v.fail("required", schemaPath, instPtr, "missing required property %q", name)
			}
		}
	}
	props, _ := s["properties"].(map[string]any)
	patterns, _ := s["patternProperties"].(map[string]any)
	compiled := make(map[string]*regexp.Regexp, len(patterns))
	for p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return &SchemaError{
				Keyword: "patternProperties", SchemaPath: schemaPath,
				Message: fmt.Sprintf("pattern %q does not compile", p),
				Err:     ErrSchemaMalformed,
			}
		}
		compiled[p] = re
	}
	matched := func(k string) bool {
		if _, ok := props[k]; ok {
			return true
		}
		for _, re := range compiled {
			if re.MatchString(k) {
				return true
			}
		}
		return false
	}
	for k, val := range obj {
		ptr := instPtr + "/" + escapePointerToken(k)
		if sub, ok := props[k]; ok {
			if err := v.validate(val, sub, ptr, schemaPath+"/properties", depth+1); err != nil {
				return err
			}
		}
		for p, re := range compiled {
			if re.MatchString(k) {
				if err := v.validate(val, patterns[p], ptr, schemaPath+"/patternProperties", depth+1); err != nil {
					return err
				}
			}
		}
	}
	switch ap := s["additionalProperties"].(type) {
	case bool:
		if !ap {
			for k := range obj {
				if !matched(k) {
					return v.fail("additionalProperties", schemaPath, instPtr+"/"+escapePointerToken(k),
						"property %q is not allowed by the schema", k)
				}
			}
		}
	case map[string]any:
		for k, val := range obj {
			if !matched(k) {
				if err := v.validate(val, ap, instPtr+"/"+escapePointerToken(k), schemaPath+"/additionalProperties", depth+1); err != nil {
					return err
				}
			}
		}
	}
	if pn, ok := s["propertyNames"]; ok {
		for k := range obj {
			if err := v.validate(k, pn, instPtr+"/"+escapePointerToken(k), schemaPath+"/propertyNames", depth+1); err != nil {
				return err
			}
		}
	}
	if dr, ok := s["dependentRequired"].(map[string]any); ok {
		for key, depsRaw := range dr {
			deps, isArr := depsRaw.([]any)
			if !isArr {
				return &SchemaError{
					Keyword: "dependentRequired", SchemaPath: schemaPath,
					Message: "dependentRequired values must be arrays of strings", Err: ErrSchemaMalformed,
				}
			}
			if _, present := obj[key]; !present {
				continue
			}
			for _, d := range deps {
				name, isStr := d.(string)
				if !isStr {
					return &SchemaError{
						Keyword: "dependentRequired", SchemaPath: schemaPath,
						Message: "dependentRequired values must be arrays of strings", Err: ErrSchemaMalformed,
					}
				}
				if _, present := obj[name]; !present {
					return v.fail("dependentRequired", schemaPath, instPtr,
						"property %q requires property %q", key, name)
				}
			}
		}
	}
	if ds, ok := s["dependentSchemas"].(map[string]any); ok {
		for key, sub := range ds {
			if _, present := obj[key]; !present {
				continue
			}
			if err := v.validate(instance, sub, instPtr, schemaPath+"/dependentSchemas", depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v *schemaValidator) checkApplicators(s map[string]any, instance any, instPtr, schemaPath string, depth int) error {
	if all, ok := s["allOf"].([]any); ok {
		for i, sub := range all {
			if err := v.validate(instance, sub, instPtr, schemaPath+"/allOf/"+strconv.Itoa(i), depth+1); err != nil {
				return err
			}
		}
	}
	if any, ok := s["anyOf"].([]any); ok {
		matched := false
		for _, sub := range any {
			if v.validate(instance, sub, instPtr, schemaPath+"/anyOf", depth+1) == nil {
				matched = true
				break
			}
		}
		if !matched {
			return v.fail("anyOf", schemaPath, instPtr, "value matches none of the anyOf alternatives")
		}
	}
	if one, ok := s["oneOf"].([]any); ok {
		matched := 0
		for _, sub := range one {
			if v.validate(instance, sub, instPtr, schemaPath+"/oneOf", depth+1) == nil {
				matched++
			}
		}
		if matched != 1 {
			return v.fail("oneOf", schemaPath, instPtr, "value matches %d of the oneOf alternatives, exactly 1 required", matched)
		}
	}
	if not, ok := s["not"]; ok {
		if v.validate(instance, not, instPtr, schemaPath+"/not", depth+1) == nil {
			return v.fail("not", schemaPath, instPtr, "value must not match the not schema")
		}
	}
	return nil
}

func (v *schemaValidator) checkConditional(s map[string]any, instance any, instPtr, schemaPath string, depth int) error {
	ifRaw, hasIf := s["if"]
	if !hasIf {
		return nil
	}
	if v.validate(instance, ifRaw, instPtr, schemaPath+"/if", depth+1) == nil {
		if then, ok := s["then"]; ok {
			return v.validate(instance, then, instPtr, schemaPath+"/then", depth+1)
		}
		return nil
	}
	if els, ok := s["else"]; ok {
		return v.validate(instance, els, instPtr, schemaPath+"/else", depth+1)
	}
	return nil
}

// formatChecks implements the asserted formats. Formats outside the map are
// annotations in draft 2020-12 and are not asserted.
var formatChecks = map[string]func(string) bool{
	"date-time": func(s string) bool {
		_, err := time.Parse(time.RFC3339Nano, s)
		return err == nil
	},
	"date": func(s string) bool {
		_, err := time.Parse("2006-01-02", s)
		return err == nil
	},
	"time": func(s string) bool {
		_, err := time.Parse("15:04:05.999999999Z07:00", s)
		return err == nil
	},
	"uuid": validUUIDFormat,
	"email": func(s string) bool {
		at := strings.LastIndexByte(s, '@')
		if at <= 0 || at == len(s)-1 {
			return false
		}
		local, domain := s[:at], s[at+1:]
		return !strings.ContainsAny(local, " \t\r\n@") && !strings.ContainsAny(domain, " \t\r\n@") &&
			strings.Contains(domain, ".")
	},
	"hostname": func(s string) bool {
		if s == "" || len(s) > 253 {
			return false
		}
		labels := strings.Split(strings.TrimSuffix(s, "."), ".")
		for _, l := range labels {
			if l == "" || len(l) > 63 {
				return false
			}
			for i := 0; i < len(l); i++ {
				c := l[i]
				ok := ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9') || c == '-'
				if !ok {
					return false
				}
			}
			if strings.HasPrefix(l, "-") || strings.HasSuffix(l, "-") {
				return false
			}
		}
		return true
	},
	"ipv4": func(s string) bool {
		ip := net.ParseIP(s)
		return ip != nil && ip.To4() != nil && strings.Count(s, ".") == 3
	},
	"ipv6": func(s string) bool {
		ip := net.ParseIP(s)
		return ip != nil && strings.Contains(s, ":")
	},
	"uri": func(s string) bool {
		u, err := url.Parse(s)
		return err == nil && u.Scheme != "" && !strings.ContainsAny(s, " \t\r\n") && !strings.Contains(s, "\\")
	},
	"json-pointer": func(s string) bool {
		if s == "" {
			return true
		}
		if !strings.HasPrefix(s, "/") {
			return false
		}
		for i := 0; i < len(s); i++ {
			if s[i] == '~' && (i+1 >= len(s) || (s[i+1] != '0' && s[i+1] != '1')) {
				return false
			}
		}
		return true
	},
	"regex": func(s string) bool {
		_, err := regexp.Compile(s)
		return err == nil
	},
}

// validUUIDFormat accepts any UUID version in canonical 8-4-4-4-12 form
// with upper or lower case hex.
func validUUIDFormat(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range []byte(s) {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !isHexByte(c) {
				return false
			}
		}
	}
	return true
}

func isHexByte(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F')
}
