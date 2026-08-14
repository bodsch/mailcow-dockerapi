# mailcow-dockerapi

Vermittler zwischen der mailcow-Oberfläche und dem Docker-Daemon, in Go.

Der Dienst ersetzt die Python-Fassung aus `original/dockerapi` ohne Anpassung
am mailcow-Frontend: gleiche Routen, gleiche JSON-Strukturen, gleicher
PubSub-Vertrag. Wo das Verhalten dennoch abweicht, steht es in
[DEVIATIONS.md](DEVIATIONS.md).

## Aufbau

```
cmd/dockerapi/          Programmeinstieg, Verdrahtung, geordnetes Beenden
internal/actions/       die 29 Container-Operationen und ihre Registry
internal/api/           HTTP-Routen und Antwortkodierung
internal/dockerclient/  Docker-Zugriff hinter einem schmalen Interface
internal/stats/         Kennzahlen von Wirt und Containern
internal/store/         Redis-Zwischenspeicher
internal/pubsub/        Empfänger für MC_CHANNEL
internal/tlsgen/        selbstsigniertes Serverzertifikat
internal/config/        Konfiguration aus der Umgebung
internal/logging/       Protokollformat der Python-Fassung
original/               die portierte Vorlage, als Referenz
```

Der Kern ist `internal/actions/registry.go`: eine Zuordnung von Namen wie
`container_post__exec__mailq__delete` auf Funktionen. Die Namen bildet mailcow
selbst, in den PubSub-Nachrichten und im PHP-Code — sie sind Teil der
Schnittstelle. Ein Test gleicht die Registry gegen die Methodennamen in
`original/dockerapi/modules/DockerApi.py` ab; fehlt eine Action, schlägt er
fehl.

## Schnittstelle

| Methode | Pfad | Zweck |
|---|---|---|
| GET | `/host/stats` | Kennzahlen des Wirtssystems |
| GET | `/containers/json?all=<bool>` | alle Container als Zuordnung Kennung → Inspect |
| GET | `/containers/{id}/json` | ein Container (nur laufende) |
| POST | `/containers/{id}/{action}` | Operation ausführen |
| POST | `/container/{id}/stats/update` | Messwerte eines Containers (Pfad im Singular) |

Der Statuscode ist stets 200; Fehler stehen im Feld `type` des Rumpfes.

Für `POST /containers/{id}/exec` benennt der Rumpf die Operation:

```json
{ "cmd": "mailq", "task": "flush" }
```

Über Redis geht dasselbe an `MC_CHANNEL`, dort mit dem Container**namen**:

```json
{
  "api_call": "container_post",
  "post_action": "exec",
  "container_name": "postfix-mailcow",
  "request": { "cmd": "mailq", "task": "flush" }
}
```

## Konfiguration

| Variable | Vorgabe | Zweck |
|---|---|---|
| `REDIS_SLAVEOF_IP` | – | ist sie gesetzt, gilt dieser Redis statt `redis-mailcow` |
| `REDIS_SLAVEOF_PORT` | `6379` | zugehöriger Port |
| `REDISPASS` | – | Redis-Passwort |
| `REDIS_DB` | `0` | Datenbanknummer |
| `REDIS_CHANNEL` | `MC_CHANNEL` | Kanal für Aufträge |
| `DBROOT` | – | MySQL-root-Passwort für die `system`-Actions |
| `DOCKER_HOST` | `unix:///var/run/docker.sock` | Docker-Endpunkt |
| `DOCKERAPI_LISTEN` | `:443` | Adresse des Servers |
| `DOCKERAPI_CERT` | `/app/dockerapi_cert.pem` | Zertifikatsdatei |
| `DOCKERAPI_KEY` | `/app/dockerapi_key.pem` | Schlüsseldatei |
| `DOCKERAPI_STATS_TIMEOUT` | `30s` | Frist beim Warten auf Messwerte |

Die ersten sechs Namen stammen aus der Python-Fassung und bleiben unverändert.

Fehlt das Zertifikatspaar, erzeugt der Dienst beim Start eines (RSA 4096,
SHA-256, 3650 Tage, `CN=dockerapi`, `subjectAltName=DNS:dockerapi`) — dieselben
Angaben, die `docker-entrypoint.sh` per `openssl` setzte.

## Bauen und Prüfen

```sh
make            # Formatierung, Prüfer, Tests
make build      # Binary
make image      # Container-Abbild
make test       # Tests mit Wettlauferkennung und Abdeckung
```

Tests, die einen echten Docker-Daemon brauchen, hängen am Marker
`integration` und laufen nicht im Regellauf:

```sh
make test-integration
```

Die Maskierung für die Shell und die Zerlegung des Docker-Stroms haben
Fuzz-Tests; die Maskierung prüft ihr Ergebnis gegen eine echte `sh`:

```sh
make fuzz
```

### Abgleich mit der Vorlage

Der eigentliche Nachweis für die Austauschbarkeit ist ein Seitenvergleich:
beide Fassungen laufen gegen denselben Docker-Daemon und dieselbe
Redis-Instanz, bekommen dieselben Anfragen, und die Antworten werden
gegenübergestellt (volatile Felder wie Zeitstempel und Auslastung vorher
normalisiert).

```sh
make compare
```

Erwartete Ausgabe: alle Routen stimmen überein, mit einer als `ERW.`
gekennzeichneten Abweichung — dem in [DEVIATIONS.md](DEVIATIONS.md) unter 1.9
beschriebenen Fehler im Original.

## Betrieb

```sh
docker run --rm \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e REDISPASS=... \
  -e DBROOT=... \
  -p 8443:443 \
  mailcow-dockerapi:latest
```

Der Dienst braucht Zugriff auf den Docker-Socket und steuert damit alle
Container des Wirts. Er gehört nicht ins offene Netz.
