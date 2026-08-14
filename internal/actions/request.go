package actions

import "encoding/json"

// Request is the decoded JSON body of a request.
//
// DockerApi.py worked on the dict directly and sometimes indexed it without
// checking (request_json['id'] in doveadm__get_acl, for instance), which raised a
// KeyError when the field was absent. The accessors here report whether a field is
// usable instead.
type Request map[string]any

// ParseRequest decodes a JSON body. An empty or invalid body yields an empty
// Request — main.py:133 did the same.
func ParseRequest(body []byte) Request {
	var r Request
	if err := json.Unmarshal(body, &r); err != nil {
		return Request{}
	}
	if r == nil {
		return Request{}
	}

	return r
}

// Has reports whether the key is present. It matches `'key' in request_json`.
func (r Request) Has(key string) bool {
	_, ok := r[key]
	return ok
}

// String returns a string value.
func (r Request) String(key string) (string, bool) {
	v, ok := r[key]
	if !ok {
		return "", false
	}

	s, ok := v.(string)
	return s, ok
}

// NonEmptyString returns a string value that is not empty.
//
// Several actions tested for truthiness in Python (`if user and mailbox`), which
// made the empty string behave like a missing field.
func (r Request) NonEmptyString(key string) (string, bool) {
	s, ok := r.String(key)
	return s, ok && s != ""
}

// Strings returns a list of strings. Elements of another type are skipped — in
// Python they would have failed the regex check further down.
func (r Request) Strings(key string) ([]string, bool) {
	v, ok := r[key]
	if !ok {
		return nil, false
	}

	raw, ok := v.([]any)
	if !ok {
		return nil, false
	}

	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}

	return out, true
}
