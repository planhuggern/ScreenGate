# ScreenGate

ScreenGate styrer skjermtid på Windows-PC-er fra en liten Go-server hjemme. Foreldreoversikten samler brukere, dagsgrenser, ukeplan, ekstratid og tilkoblede enheter. Serveren lagrer data lokalt i SQLite.

## Dette er på plass

- Felles dagskvote per bruker på tvers av PC-er.
- Egen kvote og tillatt tidsrom for hver ukedag, inkludert tidsrom over midnatt og skjermfrie dager.
- Pause og gjenåpning, samt bonusminutter som utløper ved lokal midnatt.
- Mobiltilpasset norsk kontrollpanel med gjenværende tid, enhetsstatus, syvdagershistorikk, CSV og endringslogg.
- Innlogging for administrasjonen, beskyttede skjemaer og separate, tilbakekallbare enhetsnøkler.
- Engangskoder for tilkobling av Windows-brukere. Nye brukere starter med én time per dag.
- Varsler på Windows før tiden er brukt opp, og lokal låsing når tillatelsen utløper.
- Varig klienttilstand og idempotente rapporter: omstart og tapte svar skal ikke gi ny kvote eller dobbelttelle aktivitet.
- En kort tillatelse ved nettbrudd, begrenset av gjenværende tid og neste regelendring.
- Konsistent SQLite-sikkerhetskopi som kan lastes ned fra foreldreoversikten.

Forsiden viser ingen navn eller aktivitetsdata. Registrering av programmer er avslått som standard. Serveren lagrer ikke programnavn, vindustitler, innhold eller nettleserhistorikk.

## Start med Docker

1. Kopier `.env.example` til `.env`.
2. Sett et eget `ADMIN_PASSWORD` på minst 12 tegn.
3. Kjør:

```sh
docker compose up --build -d
```

Åpne **http://localhost:8081/admin** og logg inn som `admin` med passordet ditt.

Standardoppsettet lytter bare på denne maskinen. For PC-er på hjemmenettet må `SCREENGATE_BIND` i `.env` settes til serverens LAN-adresse, eller `0.0.0.0`. Klientene trenger nettverkstilgang til serverporten. Bruk HTTPS via en reverse proxy før du sender passord eller enhetsnøkler over et nettverk du ikke stoler på.

Databasen lagres i Docker-volumet `screengate-data`. Containeren kjører som en vanlig bruker, har skrivebeskyttet rotfilsystem og har en helsesjekk. Før oppstart retter engangscontaineren `data-permissions` automatisk eierskapet til datamappen og eksisterende SQLite-filer.

**Oppgradering fra den gamle utgaven:** Ta sikkerhetskopi først. Den nye serveren krever administratorpassord og klientene må oppdateres og kobles til med engangskode. Eldre root-eide datavolumer håndteres automatisk ved vanlig Compose-oppstart. Se [drift og oppgradering](docs/OPERATIONS.md).

## Koble til en Windows-PC

1. Lag en koblingskode under **Enheter og tilkobling** i foreldreoversikten.
2. Last ned installasjonen fra samme sted.
3. På Windows-PC-en: åpne PowerShell som administrator og kjør:

```powershell
powershell -ExecutionPolicy Bypass -File .\install.ps1
```

Veiviseren spør etter serveradressen, Windows-brukeren som skal styres og koblingskoden. Eksempel på serveradresse: `http://192.168.1.10:8081/heartbeat`.

Du kan også oppgi alt eksplisitt:

```powershell
.\install.ps1 -ServerUrl "https://screengate.example/heartbeat" -User "PC\barn" -EnrollmentCode "KODEN-FRA-FORELDREOVERSIKTEN"
```

Koden varer i 15 minutter og kan brukes én gang. Navnet du valgte i foreldreoversikten bestemmer hvilken kvote enheten deler; Windows-kontonavnet trenger ikke være identisk. Lag en ny kode for hver enhet. Bruk samme ScreenGate-navn på flere PC-er når de skal dele dagskvote.

Installasjonen kontrollerer nedlastingens SHA-256, lagrer konfigurasjonen med begrensede Windows-rettigheter og oppretter én oppstartsoppgave per Windows-bruker. Oppgradering uten ny kode beholder eksisterende tilkobling. `-SkipStart` utsetter oppstart til neste innlogging.

Klienten logger i den styrte brukerens **%LOCALAPPDATA%\ScreenGate\client.log**. Konfigurasjonen ligger i **C:\ProgramData\ScreenGate\<Windows-SID>\client.json**.

### Avinstallering

Last ned `/downloads/uninstall.ps1` fra serveren, og kjør som administrator:

```powershell
.\uninstall.ps1 -User "PC\barn"
```

Dette fjerner oppgaven og enhetsnøkkelen for akkurat denne Windows-brukeren. Andre brukeres installasjoner, den delte programfilen og lokale logger blir beholdt. Trekk også tilbake enheten med **Koble fra** i foreldreoversikten. Tilbakekalling alene avinstallerer ikke klienten og vil føre til at den nekter videre skjermtilgang.

## Hvordan tiden beregnes

Klienten måler bruk av en ulåst, tilkoblet Windows-økt. En låst økt, utlogget bruker og søvn gir ikke skjermtid. Video og annen passiv skjermbruk teller som standard. Valgfritt `-idle-timeout 5m` stopper opptellingen etter fem minutter uten inndata; dette bør ikke brukes hvis passiv video skal telle.

Klienten rapporterer omtrent hvert 30. sekund og ved endring av øktstatus. Serveren bruker egne klokkeslett og begrenser rapportert aktivitet til mulig forløpt tid. Hver rapport har en unik ID, slik at et tapt svar kan prøves igjen uten dobbel belastning.

Tiden summeres per bruker. **Samtidig bruk av to PC-er bruker dobbelt så mange kvotesekunder.** Gamle rapporter fra før oppgraderingen beholder den tidligere beregningen basert på sammenhengende serverregistrerte intervaller på maksimalt 63 sekunder.

Dagsgrensen følger `SCREENGATE_TIMEZONE`, som er `Europe/Oslo` som standard. Midnatt og overgangen mellom sommer- og vintertid håndteres etter den valgte tidssonen. Forsinket aktivitet med gyldig opprinnelig dato blir belastet denne datoen.

### Regler og prioritet

1. **Pause** stenger tilgangen til du åpner igjen.
2. **Skjermfri dag / tillatt tidsrom** bestemmer om skjermen kan brukes nå.
3. **Dagsgrense + dagens ekstratid** bestemmer hvor mye tid som gjenstår.

En dagsgrense på `0` betyr ubegrenset kvote; pause og tidsplan gjelder likevel. Ekstratid omgår aldri pause eller tidsplan. Dager uten egen regel arver den vanlige dagsgrensen og tillater bruk hele døgnet.

Et tidsrom som `20:00–02:00` starter på den valgte ukedagen og fortsetter neste natt. En eksplisitt skjermfri dag stenger også for en slik videreføring. Kvoten og bonusen nullstilles ved kalenderdagens midnatt.

Endringer når en tilkoblet klient ved neste rapport, vanligvis innen 30 sekunder. Ved nettbrudd er siste tillatelse gyldig i **maksimalt 90 sekunder**, og aldri lenger enn gjenværende tid eller neste tidsgrense. Klienten lagrer tillatelsens absolutte utløp; omstart fyller den ikke opp. Uten gyldig tillatelse låser klienten Windows.

## Hva håndhevingen beskytter mot

Dette er en klient som starter i brukerens Windows-økt, ikke en Windows-tjeneste med egen privilegert håndheving. Bruk en standardkonto for den styrte brukeren og behold administratorkontoen hos en foresatt.

En teknisk kyndig bruker som stopper prosessen eller lager en egen klient, kan omgå denne modellen. Lokal tilstand er signert for å oppdage ødelagte eller endrede filer, men enhetsnøkkelen må kunne leses av den kjørende klienten. Signeringen er derfor ikke en sikkerhetsgrense mot kontoeieren. Serveren beskytter administrasjon og andre brukeres identitet; den kan ikke bevise at en kompromittert PC rapporterer sann aktivitet.

Sterkere manipulasjonsvern krever en separat Windows-tjeneste og kontopolicyer. Den installeres ikke automatisk av denne utgaven.

## Lokal utvikling

Go 1.25 eller nyere:

```powershell
$env:ADMIN_PASSWORD = (Get-Credential -UserName admin -Message 'ScreenGate-passord, minst 12 tegn').GetNetworkCredential().Password
$env:DATABASE_PATH = 'screengate.db'
go run .
```

Serveren lytter på `:8080` ved lokal kjøring. For å bygge Windows-klienten uten konsollvindu:

```powershell
go build -ldflags "-H=windowsgui" -o screengate-client.exe ./cmd/client
```

Klienten kan startes med `-config` og filen fra installasjonen. `-server`, `-user`, `-device-id` og `-token-file` finnes for kontrollert feilsøking. Ikke kjør den på en foreldre- eller utviklerkonto ved et uhell: den kan låse økten når kvoten eller tillatelsen utløper.

### Verifisering

```sh
go test ./...
go test -race ./...
go vet ./...
docker build -t screengate:local .
```

Testene dekker blant annet innlogging, CSRF, tilkobling og tilbakekalling, kvoter, ukedager, midnatt, sommertid, bonus, samtidige rapporter, offline-omstart og sikkerhetskopi.

Oppgradering fra et root-eid Docker-volum kan testes isolert på Windows:

```powershell
docker build -t screengate:verification .
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/container-upgrade-smoke.ps1
```

Testen bruker egne midlertidige containere og et eget volum. Den kontrollerer bevarte kvoter, eierskap, gjentatt oppstart og avvisning av symbolske lenker, og rydder opp testressursene etterpå.

Det finnes også en valgfri nettlesertest med Node 22+ og Playwright:

```sh
node scripts/browser-smoke.cjs
```

Bygg først serveren til `.artifacts/screengate.exe` på Windows, eller `.artifacts/screengate` på Linux. Testen lager sin egen database, kjører hele flyten for tilkobling, pause og ekstratid og tar skjermbilder av skrivebords- og mobilvisning. Den starter **aldri Windows-klienten**.

## Konfigurasjon og API

Se [driftsveiledningen](docs/OPERATIONS.md) og [API-kontrakten](docs/API.md). Databasen migreres automatisk ved oppstart. Ingen ekstern skytjeneste er nødvendig.
