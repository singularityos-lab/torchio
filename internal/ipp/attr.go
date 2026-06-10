package ipp

import "github.com/OpenPrinting/goipp"

// This file holds small readers over goipp.Attributes. Reading IPP responses
// is otherwise a verbose dance of finding an attribute by name and type
// switching its first value; these helpers keep the call sites legible.

// FindAttr returns the named attribute from a group, if present.
func FindAttr(attrs goipp.Attributes, name string) (goipp.Attribute, bool) {
	for _, a := range attrs {
		if a.Name == name {
			return a, true
		}
	}
	return goipp.Attribute{}, false
}

// FirstString returns the first value of the named attribute as a string,
// covering the several IPP tags that carry text (keyword, name, uri, charset,
// mimeMediaType, ...). Integers and enums are intentionally not stringified
// here; use FirstInt for those.
func FirstString(attrs goipp.Attributes, name string) (string, bool) {
	a, ok := FindAttr(attrs, name)
	if !ok || len(a.Values) == 0 {
		return "", false
	}
	if s, ok := a.Values[0].V.(goipp.String); ok {
		return string(s), true
	}
	return "", false
}

// Strings returns every value of the named attribute that is a string. IPP
// "*-supported" attributes are typically 1SetOf, so this is the common reader
// for capability lists (media-supported, sides-supported, ...).
func Strings(attrs goipp.Attributes, name string) []string {
	a, ok := FindAttr(attrs, name)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(a.Values))
	for _, v := range a.Values {
		if s, ok := v.V.(goipp.String); ok {
			out = append(out, string(s))
		}
	}
	return out
}

// FirstInt returns the first integer or enum value of the named attribute.
func FirstInt(attrs goipp.Attributes, name string) (int, bool) {
	a, ok := FindAttr(attrs, name)
	if !ok || len(a.Values) == 0 {
		return 0, false
	}
	if i, ok := a.Values[0].V.(goipp.Integer); ok {
		return int(i), true
	}
	return 0, false
}

// Resolutions returns every resolution value of the named attribute.
func Resolutions(attrs goipp.Attributes, name string) []goipp.Resolution {
	a, ok := FindAttr(attrs, name)
	if !ok {
		return nil
	}
	out := make([]goipp.Resolution, 0, len(a.Values))
	for _, v := range a.Values {
		if r, ok := v.V.(goipp.Resolution); ok {
			out = append(out, r)
		}
	}
	return out
}
