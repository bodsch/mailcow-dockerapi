# Abweichungen von der Python-Fassung

Die Go-Fassung ist als Ersatz ohne Anpassung am mailcow-Frontend gedacht:
gleiche Routen, gleiche JSON-Strukturen, gleicher PubSub-Vertrag, Statuscode
immer 200. Dieses Dokument hält fest, wo das Verhalten dennoch abweicht — und
warum.

Referenzen verweisen auf `original/dockerapi/`.

## 1. Behobene Fehler

Diese Stellen konnten in der Python-Fassung zu einer Ausnahme, einem
unerwünschten Kommando oder einer hängenden Anfrage führen.

### 1.1 Endloses Warten auf Messwerte

`main.py:75` und `main.py:187` warteten in `while True` darauf, dass ein
Schlüssel in Redis auftaucht, ohne Abbruchbedingung. Blieb er aus — weil Redis
nicht erreichbar war, der Sammelvorgang scheiterte oder die Container-Kennung
ungültig war — kehrte die Anfrage nie zurück.

**Jetzt:** `internal/stats` bricht nach `DOCKERAPI_STATS_TIMEOUT` (Vorgabe 30 s)
ab und meldet die Ursache des Sammelvorgangs, ersatzweise
`timeout waiting for stats`.

### 1.2 Ungültige Container-Kennung bei `/container/{id}/stats/update`

Der Handler in `main.py:178` prüfte die Kennung nicht; die Prüfung steckte in
`get_container_stats` (`DockerApi.py:547`), das bei ungültiger Eingabe nichts
nach Redis schrieb. Ergebnis war der Endlos-Wartezustand aus 1.1.

**Jetzt:** Die Prüfung erfolgt im Handler, die Antwort lautet
`{"type": "danger", "msg": "no or invalid id defined"}`.

### 1.3 `postsuper` ohne Argumente

`DockerApi.py:88` (und drei gleichartige Stellen) band das Ergebnis von
`filter()` an eine Variable und prüfte diese auf Wahrheitswert. Ein Generator
ist in Python immer wahr. Bestand `items` ausschließlich aus ungültigen
Queue-IDs, lief `postsuper` mit leerer Argumentliste.

**Jetzt:** Eine leere Auswahl liefert
`{"type": "danger", "msg": "no valid queue ids given"}`; es wird kein Kommando
abgesetzt.

### 1.4 Nicht importiertes `traceback`

`DockerApi.py:615` rief im Fehlerpfad von `exec_cmd_container`
`traceback.print_exc` auf, ohne das Modul zu importieren — der Fehlerpfad
scheiterte selbst mit einem `NameError`.

**Jetzt:** Reguläre Fehlerbehandlung mit Protokollierung.

### 1.5 Unbelegter Methodenname im PubSub-Empfang

Fehlten bei `post_action: exec` die Felder `cmd`, `task` oder `request`, blieb
`api_call_method_name` in `main.py:232` unbelegt; der folgende Zugriff löste
einen `NameError` aus.

**Jetzt:** Die Felder werden vorab geprüft; das Protokoll nennt das fehlende
Feld (`api call: cmd missing` und so weiter).

### 1.6 Wettläufe auf gemeinsamem Zustand

`host_stats_isUpdating` (Merker) und `containerIds_to_update` (Liste) wurden
aus mehreren asyncio-Aufgaben ohne Sperre verändert. `list.remove` konnte
zudem einen `ValueError` auslösen (`DockerApi.py:575`).

**Jetzt:** `internal/stats` verwaltet laufende Sammelvorgänge unter einer
Sperre. Gleichzeitige Anfragen lösen einen Vorgang aus und teilen sich das
Ergebnis. Die Tests laufen mit `-race`.

### 1.7 Ausnahmen beim Zerlegen der ACL-Ausgabe

`DockerApi.py:441` zerlegte jede Zeile mit `acl.split(maxsplit=1)` und griff
auf `split('=')[1]` zu. Eine Zeile ohne Leerraum ergab einen `ValueError`, eine
ohne Gleichheitszeichen einen `IndexError` — beides brach die gesamte Anfrage ab.

**Jetzt:** Solche Zeilen werden übergangen, die übrigen Einträge kommen durch.

### 1.8 `postcat` ohne Treffer

`DockerApi.py:126` prüfte `postcat_return`, das bei leerer Trefferliste nie
zugewiesen wurde — ein `NameError`.

**Jetzt:** Es gilt die Antwort aus 2.1.

### 1.9 Unbekannte Action meldete einen internen Python-Fehler

`main.py:159` hinterlegte für einen nicht auflösbaren Namen einen Ersatzaufruf:

```python
api_call_method = getattr(dockerapi, name, lambda container_id: Response(...))
return api_call_method(request_json, container_id=container_id)
```

Der Ersatzaufruf nimmt einen Positionsparameter namens `container_id`, wird
aber mit `request_json` an dieser Position **und** zusätzlich mit
`container_id=` als Schlüsselwort aufgerufen. Python bricht mit
`TypeError: got multiple values for argument 'container_id'` ab; die
umgebende Fehlerbehandlung reicht diesen Text als `msg` weiter.

Die vorgesehene Meldung `container_post - unknown api call` erschien dadurch
nie. Der Vergleichslauf gegen die laufende Python-Fassung liefert:

```
py: {"type": "danger", "msg": "post_containers.<locals>.<lambda>() got multiple values for argument 'container_id'"}
go: {"type": "danger", "msg": "container_post - unknown api call"}
```

**Jetzt:** Die im Original vorgesehene Meldung.

## 2. Sichtbare Verhaltensänderungen

Diese Punkte ändern die Antwort in Fällen, in denen die Python-Fassung kein
brauchbares Ergebnis lieferte.

### 2.1 Kein passender Container

Die meisten Actions kehrten innerhalb der Schleife über die Trefferliste
zurück. War die Liste leer, lieferte die Funktion implizit `None`, und der
HTTP-Rumpf lautete `null`.

**Jetzt:** `{"type": "danger", "msg": "no container found"}`.

Ausgenommen sind `stop`, `start` und `restart`: sie melden weiterhin Erfolg,
auch wenn nichts zutraf. mailcow stoppt darüber Container, die bereits
gestoppt sind.

### 2.2 Fehlende Pflichtfelder

Fehlte ein Feld im Rumpf, endete die Action in Python entweder mit einem
`KeyError` (abgefangen zu `{"type": "danger", "msg": "'feldname'"}`) oder
lieferte `null`.

**Jetzt:** Eine benannte Meldung, etwa
`{"type": "danger", "msg": "maildir is missing"}`.

### 2.3 `container_post__stats` enthält `precpu_stats`

`DockerApi.py:76` entnahm den ersten Datensatz eines laufenden Stream. Dieser
enthält noch keine Vormessung, weshalb sich daraus keine CPU-Auslastung
berechnen ließ.

**Jetzt:** Es wird dieselbe Abfrage verwendet wie für
`/container/{id}/stats/update` — mit gefülltem `precpu_stats`. Die Antwort
enthält damit mehr, nicht weniger.

### 2.4 Maskierte Werte in der ACL-Antwort

`DockerApi.py:423` maskierte `id`, `user` und `mailbox` für die Shell und
übernahm die maskierten Zeichenketten anschließend unverändert in die
JSON-Antwort. Ein Postfach mit Anführungszeichen erschien dort verfälscht.

**Jetzt:** Die Maskierung betrifft nur das Kommando; die Antwort führt die
Originalwerte.

### 2.5 Reihenfolge in `/containers/json`

Python behielt die Einfügereihenfolge der Container bei, Gos JSON-Kodierung
sortiert Objektschlüssel. Für die Auswertung ist das ohne Belang — `json_decode`
in PHP liefert ein assoziatives Array.

## 3. Bewusst beibehaltene Eigenheiten

Diese Punkte wirken wie Fehler, bleiben aber unverändert, weil das Frontend
darauf aufbaut.

- **`system__df` liefert eine nackte Zeichenkette.** FastAPI kodierte den
  Rückgabewert seinerseits als JSON, der Rumpf lautet also `"50G,20G,..."`
  einschließlich Anführungszeichen — anders als bei jeder anderen Action.
  Im Fehlerfall `"0,0,0,0,0,0"`.
- **Statuscode immer 200.** Auch Fehler kommen mit 200; ausgewertet wird das
  Feld `type`.
- **`maildir__move` hängt `_index` nur an das Ziel an.** Quelle ist
  `/var/vmail_index/<name>`, Ziel `/var/vmail_index/<name>_index`
  (`DockerApi.py:363`).
- **`mailq__deliver` prüft die Exit-Codes nicht** und meldet stets
  `Scheduled immediate delivery` (`DockerApi.py:160`).
- **Der Namensraum der Actions** (`container_post__exec__mailq__delete` und so
  weiter) bleibt Zeichen für Zeichen erhalten; ein Test gleicht ihn gegen
  `original/dockerapi/modules/DockerApi.py` ab.
- **Feldreihenfolge und Einrückung** der JSON-Antworten entsprechen
  `json.dumps(..., indent=4)`.

## 4. Technische Unterschiede ohne Auswirkung auf die Schnittstelle

- **Ein Docker-Client statt zwei.** Die Python-Fassung hielt `docker` und
  `aiodocker` parallel.
- **Kommandos ohne Shell, wo möglich.** Wo keine Pipe, Umleitung oder
  Bedingung nötig ist, geht das Argv direkt an `docker exec`. Für `system__df`,
  `system__mysql_tzinfo_to_sql`, `maildir__cleanup`, `maildir__move` und den
  Rspamd-Wechsel bleibt eine Shell nötig; dort maskiert eine getestete Funktion
  (`internal/actions/shell.go`, mit Fuzz-Test gegen eine echte `sh`).
- **`\W` bei `maildir__cleanup`.** Pythons `\W` ist Unicode-bewusst, Gos nicht.
  Die Zeichenklasse ist deshalb als `[^\p{L}\p{N}_]+` ausgeschrieben, damit
  Postfächer mit Umlauten denselben Verzeichnisnamen ergeben.
- **Queue-ID-Prüfung etwas strenger.** Pythons `$` in `^[0-9a-fA-F]+$` lässt
  einen abschließenden Zeilenumbruch zu, Gos `$` nicht.
- **`isalnum` auf ASCII beschränkt.** Pythons `str.isalnum()` akzeptiert
  Buchstaben aller Schriftsysteme. Docker-Kennungen sind hexadezimal; für
  gültige Eingaben ändert sich nichts.
- **Zertifikat aus dem Programm.** `docker-entrypoint.sh` und `openssl`
  entfallen; `internal/tlsgen` erzeugt dasselbe Material (RSA 4096, SHA-256,
  3650 Tage, `CN=dockerapi`, `O=mailcow`, `subjectAltName=DNS:dockerapi`).
  Ein vorhandenes Paar wird weiterverwendet.
- **Begrenzter Anfragerumpf.** Höchstens 4 MiB; FastAPI kannte keine Grenze.
- **Geordnetes Beenden** bei SIGINT und SIGTERM.
