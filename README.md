# ScreenGate

En minimal Go-server for å motta heartbeats fra Windows-klienter. Den summerer sekunder per bruker i SQLite, logger aktiviteten og svarer alltid med `allow`. Docker Compose lagrer databasen i en persistent volume.

## Kjør med Docker

```sh
docker compose up --build
```

Serveren lytter på `http://localhost:8081`.

Åpne `http://localhost:8081/` for en enkel oversikt over dagens aktivitet.

Bruk knappen **Last ned installasjon for Windows** på forsiden for å hente `install.ps1`. Kjør skriptet som administrator på Windows 11 x64-maskinen; det viser en liste over kjente brukerprofiler. Velg brukeren som skal kjøre klienten.

Klienten kjører uten konsollvindu. Statusendringer logges til `C:\ProgramData\ScreenGate\client.log`; loggfilen roteres ved 1 MB.

```powershell
powershell -ExecutionPolicy Bypass -File .\install.ps1
```

### Avinstaller

Kjør i PowerShell som administrator for å fjerne oppstartsoppgaven og klientfilene:

```powershell
Unregister-ScheduledTask -TaskName "ScreenGate Client" -Confirm:$false; Remove-Item "C:\Program Files\ScreenGate" -Recurse -Force
```

Forsiden viser bruk per bruker og lar deg sette en daglig kvote i timer og minutter. `0 t 0 min` betyr ubegrenset. Når dagens bruk når kvoten, svarer serveren med `lock` ved neste heartbeat.

## Send en heartbeat

```sh
curl -X POST http://localhost:8081/heartbeat \
  -H "Content-Type: application/json" \
  -d '{"device_id":"pc-barn1","user":"barn1","active_seconds":47,"reported_at":"2026-08-30T21:30:00+02:00"}'
```

Respons:

```json
{"action":"allow","message":"ok","daily_total_seconds":47}
```

## Tester

```sh
go test ./...
```

## Windows-klient

Klienten sender én heartbeat med `active_seconds: 30` hvert 30. sekund mens den kjører i den innloggede Windows-økten. Bygg og kjør den på Windows:

```powershell
go build -ldflags "-H=windowsgui" -o screengate-client.exe ./cmd/client
.\screengate-client.exe -server http://SERVER:8081/heartbeat
```

---

## Videre idéer

Hovedidé

Arkitekturen består av:

én sentral server som kjører hjemme
én liten klient installert på hver Windows-PC
serveren bestemmer reglene
klienten rapporterer aktivitet og utfører kommandoene den får fra serveren

Klienten skal være så enkel og generell som mulig. Regler og logikk skal i hovedsak ligge på serveren, slik at klientene normalt bare trenger å installeres én gang.

Første versjon

Første versjon skal være svært enkel.

Serveren skal:

eksponere POST /heartbeat
motta aktivitet fra klientene
logge aktiviteten
alltid svare at bruk er tillatt

Eksempel på request:

{
  "device_id": "pc-barn1",
  "user": "barn1",
  "active_seconds": 47
}

Eksempel på response:

{
  "action": "allow",
  "message": "ok"
}

Klienten skal etter hvert sende heartbeat omtrent én gang per minutt.

Første milepæl er bare å få hele kjeden til å virke stabilt:

Windows-klient
    ↓
POST /heartbeat
    ↓
ScreenGate-server
    ↓
logging
    ↓
ALLOW
Fremtidig arkitektur

På sikt skal serveren holde oversikt over:

brukere
enheter
hvor mye skjermtid som er brukt
gjenværende skjermtid
regler per bruker
regler per ukedag
bonusminutter
midlertidige overstyringer
eventuell manuell låsing

Eksempel på regel:

Mandag–fredag:

før kl. 12:00
    60 minutter tilgjengelig

etter kl. 12:00
    60 nye minutter tilgjengelig

Tiden skal være kvotebasert, ikke basert på faste spilleperioder.

Eksempel:

08:10–08:30 -> 20 minutter brukt
09:15–09:35 -> 20 minutter brukt

20 minutter gjenstår før kl. 12

Klokken 12 starter en ny kvote.

Klientens ansvar

Windows-klienten skal være en liten agent som kjører i bakgrunnen.

Den skal på sikt kunne:

identifisere maskinen
identifisere aktiv Windows-bruker
måle faktisk brukeraktivitet
sende aktivitet til serveren omtrent én gang per minutt
motta beslutning fra serveren
vise advarsler
låse PC-en når serveren krever det

Klienten skal ikke inneholde kompleks regelmotor.

Den skal primært forstå enkle handlinger som:

ALLOW
WARN
LOCK
Offline-fallback

Systemet skal fortsatt fungere dersom hjemmeserveren eller nettverket midlertidig er utilgjengelig.

Klienten skal derfor lagre siste gyldige svar fra serveren.

Eksempel:

Server:
23 minutter gjenstår

Serveren blir utilgjengelig.

Klienten kan fortsette lokalt i maksimalt de 23 minuttene.
Når tiden er brukt opp:
LOCK

Det skal også finnes en maksimal offline-tillatelse, for eksempel 60 minutter.

En klient skal aldri få en ny full kvote bare fordi kontakten med serveren forsvinner.

Konseptuelt:

offline_remaining =
    min(last_server_remaining, max_offline_allowance)
Flere enheter

På sikt bør skjermtid kunne følge brukeren og ikke bare maskinen.

Eksempel:

Barn 1 bruker:
40 minutter på desktop
10 minutter på laptop

Totalt brukt:
50 minutter

Gjenværende kvote:
10 minutter

Dette betyr at serveren er autoritativ for samlet bruk.

Teknologi

Foreløpig ønsket stack:

Server
Go
Go standard library der det er praktisk
HTTP/JSON API
Docker
Docker Compose

Database kommer senere når det faktisk er behov for det.

SQLite er et naturlig førstevalg.

Klient
Go
Windows
etter hvert Windows Service
Prinsipper

Prosjektet skal være:

enkelt
robust
lett å forstå
lett å feilsøke
avhengig av få komponenter
uten unødvendige abstraksjoner
bygget iterativt

Unngå å bygge funksjonalitet før den trengs.

Ikke lag komplekse frameworks, plugin-systemer eller generiske abstraheringer uten et konkret behov.

Utviklingsrekkefølge

Planlagt rekkefølge er omtrent:

Minimal server med /heartbeat
Enkel klient som sender heartbeat
Stabil logging av aktivitet
Summering av brukt tid
Server returnerer gjenværende tid
Enkle tidskvoter
Klienten kan vise advarsler
Klienten kan låse Windows
Offline-fallback
Persistens med SQLite
Enkel administrasjonsside
Bonusminutter og midlertidige overrides

Hver milepæl bør være liten og fungerende før neste bygges.

Nåværende scope

Akkurat nå skal prosjektet kun fokusere på den minimale serveren.

Ikke implementer ennå:

database
autentisering
skjermtidsregler
låsing
frontend
administrasjonsside
avansert konfigurasjon

Første mål er kun:

POST /heartbeat
-> valider input
-> logg aktivitet
-> returner ALLOW

Dette skal være et lite og enkelt fundament som resten av ScreenGate senere kan bygges på.
