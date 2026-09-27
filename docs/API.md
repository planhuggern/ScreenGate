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

## Klientoppdateringer

- `GET /downloads/update-key.json`: offentlig RSA-nøkkel med Base64-feltene `modulus` og `exponent`. Installasjonen lagrer denne én gang.
- `GET /downloads/update.json?nonce=<64 heksadesimale tegn>`: signert manifest med Base64-feltene `payload` og `signature`. Signaturen er RSA PKCS#1 v1.5 med SHA-256 over de eksakte dekodede payload-bytene. Payload er JSON med `protocol: 1`, samme `nonce`, `sha256` og `size` for klientfilen. Klienten må kontrollere signatur og utfordring før bruk.
- `GET /downloads/screengate-client.exe`: filen manifestet beskriver. Hvis filen ble byttet mellom manifest og nedlasting, avvises nedlastingen og neste sjekk prøver igjen.

Disse endepunktene er offentlige; privat signeringsnøkkel eller enhetsnøkler sendes ikke. Manifest og offentlig nøkkel returneres med `Cache-Control: no-store`.

## Rapport: `POST /heartbeat`

```json
{
  "device_id":"Nora-PC",
  "user":"Nora",
  "heartbeat_id":"tilfeldig-unik-id-per-rapport",
  "locked":false
}
```

- `locked` er `true` mens Windows er låst, ellers `false`. Klienten sender heartbeat ved endring og hvert 30. sekund, også mens den er låst.
- Serveren beregner bruk fra sine egne mottakstidspunkter. Første heartbeat starter målingen uten belastning. Intervallet frem til neste heartbeat telles bare hvis forrige status var ulåst. Dermed avslutter låsing et bruksintervall, og opplåsing starter et nytt.
- Opphold på mer enn 63 sekunder belastes ikke. Tid uten kontakt etterregistreres ikke. Ulåst tid telles også uten tastatur- eller museaktivitet.
- `heartbeat_id` identifiserer en rapport. Samme rapport kan prøves igjen med samme ID; endret låsestatus må ha ny ID.
- For eldre klienter godtas `session_state` (`active`, `idle`, `locked`). `locked` har forrang når feltet finnes. Manglende status regnes som ulåst.
- Eldre felter `active_seconds`, `activity_date` og `reported_at` ignoreres i tidsberegningen. Intervaller fordeles over døgn og timer etter serverens tidssone.

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
  "next_transition_at":"2026-09-19T00:00:00+02:00",
  "next_lock_at":"0001-01-01T00:00:00Z"
}
```

`action` er `allow` eller `lock`. `reason` er `allowed`, `paused`, `schedule` eller `quota`. `quota_seconds=0` og `unlimited=true` betyr ubegrenset dagskvote, men ikke ubegrenset offline-tillatelse. `remaining_seconds=0` alene kan derfor ikke brukes til å bestemme om brukeren er låst.

Klienten må avslutte tillatelsen ved det tidligste av:

1. Oppbrukt gjenværende kvote.
2. Utløpt `lease_seconds`, beregnet fra da forespørselen ble sendt.
3. Neste regelendring i `next_transition_at`.

Ukjent tidspunkt angis med Go sin nulltid (`0001-01-01T00:00:00Z`). Ikke bruk denne som en faktisk planlagt åpning. Ved `lock` er tillatelsen null sekunder; `next_allowed_at` kan forklare neste planlagte åpning. Manuell pause har ingen automatisk åpning.

`next_lock_at` angir når tidsplanen vil stenge tilgangen. Klienten bruker dette til varsler også når dagskvoten er ubegrenset. En vanlig nullstilling av kvoten ved midnatt gir ikke et låsevarsel.

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
