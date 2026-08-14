package actions

import "encoding/json"

// Request ist der dekodierte JSON-Rumpf einer Anfrage.
//
// DockerApi.py arbeitete direkt auf dem dict und griff teilweise ohne
// Existenzprüfung zu (etwa request_json['id'] in doveadm__get_acl), was bei
// fehlendem Feld zu einem KeyError führte. Die Zugriffsmethoden hier melden
// stattdessen, ob ein Feld nutzbar ist.
type Request map[string]any

// ParseRequest dekodiert einen JSON-Rumpf. Ein leerer oder ungültiger Rumpf
// ergibt eine leere Request – main.py:133 verfuhr genauso.
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

// Has meldet, ob der Schlüssel vorhanden ist. Entspricht `'key' in request_json`.
func (r Request) Has(key string) bool {
	_, ok := r[key]
	return ok
}

// String liefert einen Zeichenkettenwert.
func (r Request) String(key string) (string, bool) {
	v, ok := r[key]
	if !ok {
		return "", false
	}

	s, ok := v.(string)
	return s, ok
}

// NonEmptyString liefert einen Zeichenkettenwert, der nicht leer ist.
//
// Mehrere Actions prüften in Python auf Wahrheitswert (`if user and mailbox`),
// wodurch der leere String wie ein fehlendes Feld wirkte.
func (r Request) NonEmptyString(key string) (string, bool) {
	s, ok := r.String(key)
	return s, ok && s != ""
}

// Strings liefert eine Liste von Zeichenketten. Elemente anderen Typs werden
// übergangen – in Python wären sie an der nachgelagerten Regex-Prüfung
// gescheitert.
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
