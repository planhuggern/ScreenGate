# API-kontrakt

Klienter bruker JSON over HTTP eller HTTPS. Alle personlige klientendepunkter krever `Authorization: Bearer <enhetsnøkkel>`. Nøkler er bundet til én logisk bruker og ett `device_id`; serveren avviser forsøk på å sende rapporter for andre identiteter.

## Tilkobling: `POST /enroll`

Koblingskoden opprettes av en innlogget administrator, varer i 15 minutter og kan brukes én gang. En ny kode for samme bruker erstatter en eventuell ubrukt kode.

```json
{"code":"AAAA-BBBB-CCCC-DDDD","device_id":"Nora-PC","user":"PC\\windowsbruker"}
```

`user` i forespørselen er bare informasjon fra installasjonen. Brukeren som ble valgt da koden ble opprettet er autoritativ.

```json
{"token":"64-heksadesimale-tegn","device_id":"Nora-PC","user":"Nora"}
```

Lagre tokenet i beskyttet klientkonfigurasjon. Serveren lagrer bare en SHA-256-hash. Ny tilkobling for samme bruker og maskin tilbakekaller den tidligere nøkkelen.

## Rapport: `POST /heartbeat`

```json
{
  "device_id":"Nora-PC",
  "user":"Nora",
  "heartbeat_id":"tilfeldig-unik-id-per-rapport",
  "session_state":"active",
  "active_seconds":30,
  "activity_date":"2026-09-18",
  "reported_at":"2026-09-18T15:00:00+02:00"
}
```

- `heartbeat_id` beholdes når samme rapport prøves igjen. Ny bruk som oppstår mens en rapport venter på svar legges i neste rapport.
- `session_state` er `active`, `idle` eller `locked`. Klienten fortsetter å kontakte serveren mens økten er låst, slik at nye regler kan hentes.
- `active_seconds` er målt bruk siden forrige rapport ble opprettet, mellom 0 og 86400. Serveren begrenser summen til tilgjengelig forløpt servertid. Første kontakt etablerer målingens start og belastes ikke.
- `activity_date` er datoen fra den sist gyldige serverbeslutningen. Det gjør at en forsinket rapport fortsatt kan belastes den opprinnelige dagen. Dato i fremtiden eller aktivitet før enheten fantes blir avvist. Rapporten kan prøves igjen etter flere dagers fravær.
- `reported_at` brukes ikke som autoritativ klokke. Serveren bruker mottakstidspunktet sitt.

Eksempel på tillatelse:

```json
{
  "action":"allow",
  "message":"ok",
  "daily_total_seconds":1800,
  "policy_version":3,
  "remaining_seconds":1800,
  "quota_seconds":3600,
  "unlimited":false,
  "lease_seconds":90,
  "reason":"allowed",
  "server_time":"2026-09-18T15:00:00+02:00",
  "policy_date":"2026-09-18",
  "next_allowed_at":"0001-01-01T00:00:00Z",
  "next_transition_at":"2026-09-19T00:00:00+02:00"
}
```

`action` er `allow` eller `lock`. `reason` er `allowed`, `paused`, `schedule` eller `quota`. `quota_seconds=0` og `unlimited=true` betyr ubegrenset dagskvote, men ikke ubegrenset offline-tillatelse. `remaining_seconds=0` alene kan derfor ikke brukes til å bestemme om brukeren er låst.

Klienten må avslutte tillatelsen ved det tidligste av:

1. Oppbrukt gjenværende kvote.
2. Utløpt `lease_seconds`, beregnet fra da forespørselen ble sendt.
3. Neste regelendring i `next_transition_at`.

Ukjent tidspunkt angis med Go sin nulltid (`0001-01-01T00:00:00Z`). Ikke bruk denne som en faktisk planlagt åpning. Ved `lock` er tillatelsen null sekunder; `next_allowed_at` kan forklare neste planlagte åpning. Manuell pause har ingen automatisk åpning.

En rapport med samme ID belastes bare én gang. Serveren beregner likevel en ny beslutning fra gjeldende regler, slik at et nytt pausevedtak ikke skjules av en eldre rapport.

## Feil betyr ikke ny tillatelse

Feil returneres som riktig HTTP-status og et objekt som `{"error":"invalid heartbeat"}`. De inneholder aldri `action: allow`.

| Status | Betydning |
| --- | --- |
| 400 | Ugyldig JSON, identitet, dato eller verdi |
| 401 | Manglende/tilbakekalt nøkkel eller ugyldig koblingskode |
| 403 | Nøkkelen og rapportens bruker/maskin stemmer ikke |
| 405 | Feil HTTP-metode |
| 429 | For mange forsøk; følg `Retry-After` |
| 503 | Serveren kan ikke gi en pålitelig beslutning |

401/403 tilbakekaller også klientens lokale tillatelse. Ved nettverksfeil og øvrige serverfeil gjelder bare allerede lagret tillatelse frem til dens opprinnelige utløp.

## Andre endepunkter

- `GET /healthz`: database- og serverstatus uten persondata.
- `GET /`: offentlig landingsside uten persondata.
- `GET /downloads/install.ps1`, `/downloads/uninstall.ps1`, `/downloads/screengate-client.exe` og `/downloads/screengate-client.exe.sha256`: installasjonsfiler uten nøkler.
- `POST /event`: kompatibilitet med valgfri rapportering av programskifter. Krever samme enhetsautentisering og identitetskontroll; programnavn blir ikke lagret eller logget.
- Administrasjonsruter under `ADMIN_PATH`: HTTP Basic-innlogging. Alle endrende skjemakall krever `csrf_token` fra kontrollpanelet. `/export.csv` gir syvdagersoversikt og `/backup` gir en konsistent SQLite-kopi.

Forespørsler er begrenset til 16 KiB. Klienten bør ikke følge HTTP-omdirigeringer, og må validere status, JSON, handling og tillatelsens grenser før en tidligere beslutning erstattes.
