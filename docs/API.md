# API-kontrakt

Klienter bruker JSON over HTTP eller HTTPS. Alle personlige klientendepunkter krever `Authorization: Bearer <enhetsnøkkel>`. Nøkler er bundet til én logisk bruker og ett `device_id`; serveren avviser forsøk på å sende rapporter for andre identiteter.

## Tilkobling: `POST /enroll`

En innlogget administrator oppgir brukernavn og velger **Last ned installasjon**. Skjemaet sender en CSRF-beskyttet `POST` til administrasjonens `/devices/pair`, som oppretter en koblingskode og returnerer `install.ps1` som nedlasting med koden inkludert. Svaret har `Cache-Control: no-store`; koden legges ikke i URL-en. Koden varer i 15 minutter og kan brukes én gang. En ny nedlasting for samme bruker erstatter en eventuell ubrukt kode.

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
  "client_version":"2026.10.28.3",
  "locked":false,
  "usage":{
    "id":"unik-id-for-forbruksrapporten",
    "remaining_seconds":900,
    "quota_seconds":1800,
    "policy_date":"2026-10-28",
    "intervals":[{"start":"2026-10-28T10:00:00Z","end":"2026-10-28T10:10:00Z"}]
  }
}
```

- `client_version` identifiserer klientutgaven. Samme versjons-ID sendes i `User-Agent: ScreenGate-Client/2026.10.28.3` ved heartbeat og programrapporter. Eldre klienter kan utelate feltet.
- `locked` er `true` mens Windows er låst, ellers `false`. Klienten sender heartbeat ved endring og hvert 30. sekund, også mens den er låst.
- Med `usage` belaster serveren de målte intervallene én gang og legger ikke også til mottaksintervaller. Forsinket bruk registreres på faktisk dato/time etter serverens tidssone. Ulåst tid telles også uten tastatur- eller museaktivitet.
- Uten `usage` beholdes eldre beregning fra serverens mottakstidspunkter: første rapport starter målingen, og neste rapport belaster intervallet hvis forrige status var ulåst og oppholdet er høyst 63 sekunder. Slike eldre rapporter etterregistrerer ikke tid fra lengre nettbrudd.
- `heartbeat_id` identifiserer en rapport. Samme rapport kan prøves igjen med samme ID; endret låsestatus må ha ny ID.
- For eldre klienter godtas `session_state` (`active`, `idle`, `locked`). `locked` har forrang når feltet finnes. Manglende status regnes som ulåst.
- Eldre felter `active_seconds`, `activity_date` og `reported_at` ignoreres i tidsberegningen. Intervaller fordeles over døgn og timer etter serverens tidssone.

### Bekreftet lokal bruk fra klient 2026.10.28.2

`usage.id` tilhører en uforanderlig forbruksrapport og beholdes ved gjentatte sendinger, også når en ny låsestatus gir ny `heartbeat_id`. Intervallene skal være ordnet uten innbyrdes overlapp, ha positiv varighet på høyst én time og ikke ende mer enn fem minutter foran serverens mottakstid. Høyst 32 intervaller sendes om gangen. Klienten justerer sin klokke mot serveren og lagrer rapporten før sending.

`remaining_seconds`, `quota_seconds` og `policy_date` er øyeblikksbildet da forbruksrapporten ble laget. Serveren tar med de målte intervallene og beregner felles restkvote fra gjeldende regler og alle enheters forbruk. Den overskriver ikke felleskvoten med én enhets resttall. En eldre tilstandsfil kan sende `legacy:true` med tom intervalliste for å etablere et minste kjent forbruk fra gammel kvote og restkvote; se begrensningene i [steg 4-status](STEP_4.md).

Et vellykket svar inneholder `usage_ack` lik `usage.id`, samt `timezone` for kalenderberegningen. Dette bekrefter at forbruket er lagret, selv om handlingen er `lock`. Feil ved lagring gir ingen slik bekreftelse. Samme ID med endret innhold avvises. Ny klient godtar ikke svaret som ny autoritet hvis bekreftelsen mangler eller er feil. Bruk som ble registrert etter rapportens øyeblikksbilde forblir utestående og reduserer lokal restkvote til det også er bekreftet.

Eksempel på tillatelse:

```json
{
  "action":"allow",
  "message":"ok",
  "daily_total_seconds":900,
  "policy_version":3,
  "remaining_seconds":900,
  "quota_seconds":1800,
  "unlimited":false,
  "lease_seconds":90,
  "reason":"allowed",
  "server_time":"2026-10-28T11:10:00+01:00",
  "policy_date":"2026-10-28",
  "usage_ack":"unik-id-for-forbruksrapporten",
  "timezone":"Europe/Oslo",
  "policy":{
    "daily_quota_seconds":1800,
    "paused":false,
    "weekdays":[],
    "bonus_seconds":0,
    "bonus_date":"2026-10-28"
  },
  "next_allowed_at":"0001-01-01T00:00:00Z",
  "next_transition_at":"2026-10-29T00:00:00+01:00",
  "next_lock_at":"0001-01-01T00:00:00Z"
}
```

`action` er `allow` eller `lock`. `reason` er `allowed`, `paused`, `schedule` eller `quota`. `quota_seconds=0` og `unlimited=true` betyr ubegrenset dagskvote; pause og tidsplan gjelder fortsatt. `remaining_seconds=0` alene kan derfor ikke brukes til å bestemme om brukeren er låst.

Fra klientversjon **2026.10.28.1** kontrollerer klienten:

1. At en tidligere gyldig serverbeslutning finnes og tillater bruk.
2. At lokal restkvote gjenstår når dagskvoten er begrenset.
3. At en kjent stengetid i `next_lock_at` ikke er passert.

Ved kontaktfeil beholdes beslutning, restkvote og stengetid. `lease_seconds` og `next_transition_at` sendes fortsatt i eksisterende svarformat, men utløp av den korte leasen stenger ikke alene denne klientversjonen. Avstemming er innført fra `2026.10.28.2`; lagret ukeplan og ny dagskvote uten nett er innført fra `2026.10.28.3`. Se [steg 4-status](STEP_4.md). Eldre klienter kan ha annen feiloppførsel.

### Lagret ukeplan fra klient 2026.10.28.3

`policy` inneholder reglene som brukes lokalt ved kontaktbrudd. `daily_quota_seconds` er standardkvoten, `paused` gjelder uten sluttdato, og `bonus_seconds` gjelder bare `bonus_date`. Bonus øker bare en begrenset kvote. `weekdays` inneholder normalt syv objekter med feltene `weekday` (søndag=0), `configured`, `quota_seconds`, `disabled`, `start_minute` og `end_minute`.

En ukedag som mangler eller har `configured=false` arver standardkvoten og tillater hele dagen. En konfigurert dag bruker egen kvote og tidsvindu. `disabled=true` stenger hele den kalenderdagen. Tidsvinduet inkluderer startminuttet og utelukker sluttminuttet; `end_minute=1440` betyr slutten av dagen. Start større enn slutt betyr et vindu over midnatt. En uttrykkelig stengt påfølgende dag overstyrer dette vinduet. Lik start og slutt er ugyldig; hele dagen angis med 0–1440.

Klienten følger serverens navngitte `timezone`, normalt `Europe/Oslo`, og teller målt bruk på riktig dato. Ved midnatt gis den nye ukedagens kvote uten gårsdagens bonus. Dagens restkvote fylles ikke opp ved lokale regelvurderinger eller omstart. Et gyldig serversvar overtar fortsatt felles restkvote etter avstemming. Klienten kontrollerer at regelobjektets dato, kvote og handling stemmer med svaret. Et eldre svar uten `policy` gir ingen full ukeplan for fremtidige døgn; eksisterende restkvote og kjent stengetid brukes da som før.

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

401/403 tilbakekaller også klientens lokale tillatelse, uansett tidligere kontaktfeil. Ved nettverksfeil og øvrige serverfeil beholdes siste gyldige beslutning; lokalt forbruk trekkes fra restkvoten. Feilen gir ingen ny kvote og opphever ikke en tidligere stenging.

## Andre endepunkter

- `GET /healthz`: database- og serverstatus uten persondata.
- `GET /`: offentlig landingsside uten persondata.
- `GET /downloads/install.ps1`, `/downloads/uninstall.ps1`, `/downloads/screengate-client.exe` og `/downloads/screengate-client.exe.sha256`: installasjonsfiler uten nøkler.
- `POST /event`: kompatibilitet med valgfri rapportering av programskifter. Krever samme enhetsautentisering og identitetskontroll; programnavn blir ikke lagret eller logget.
- Administrasjonsruter under `ADMIN_PATH`: HTTP Basic-innlogging. Alle endrende skjemakall krever `csrf_token` fra kontrollpanelet. `/export.csv` gir syvdagersoversikt og `/backup` gir en konsistent SQLite-kopi.

Forespørsler er begrenset til 16 KiB. Klienten bør ikke følge HTTP-omdirigeringer, og må validere status, JSON, handling og tillatelsens grenser før en tidligere beslutning erstattes.
