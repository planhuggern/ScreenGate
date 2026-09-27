# Drift, oppgradering og feilsøking

## Innstillinger

| Miljøvariabel | Standard | Bruk |
| --- | --- | --- |
| `ADMIN_PASSWORD` | Ingen | Påkrevd, minst 12 tegn |
| `ADMIN_USER` | `admin` | Brukernavn for foreldreoversikten |
| `ADMIN_PATH` | `/admin` | Én sti-komponent, for eksempel `/familie`; erstatter ikke innlogging |
| `DATABASE_PATH` | `screengate.db` | Databasefil; Compose bruker `/data/screengate.db` |
| `SCREENGATE_TIMEZONE` | `Europe/Oslo` | Tidssone for kalenderdager og ukeplan |
| `SCREENGATE_TRUSTED_ORIGINS` | Tom | Eksakte origins for reverse proxy, for eksempel `https://screen.example.no` |
| `LISTEN_ADDR` | `:8080` | Lokal serveradresse |
| `CLIENT_BINARY_PATH` | `/client/screengate-client.exe` eller lokal fil | Nedlastbar Windows-klient |
| `SCREENGATE_BIND` | `127.0.0.1` | Compose: hvilken vertsadresse som eksponeres |
| `SCREENGATE_PORT` | `8081` | Compose: port på verten |

`.env` leses av Docker Compose. `go run .` bruker miljøvariablene fra skallet og leser ikke filen automatisk. Den lokale `.env`-filen og databaser er ignorert av Git og Docker-bygget.

Foreldreoversikten bruker nettleserens HTTP Basic-dialog. Velg en egen lang passordfrase. HTTPS håndteres normalt av en reverse proxy. Proxyen må videresende opprinnelig `Host`, `Authorization` og `Origin`; serverens kontroll av forespørsler fra andre nettsider skal ikke skrus av. Innloggingsforsøk begrenses etter direkte klient-IP; bak en proxy kan flere klienter dele denne grensen.

## Oppgradering fra første ScreenGate-versjon

1. Stopp den gamle serveren og ta en kopi av hele det vedvarende datavolumet. Ved filbasert kopi må serveren være stoppet; SQLite kan ha nyere data i `-wal`-filen.
2. Lag `.env` med eget administratorpassord. Den gamle skjulte adminstien kan beholdes gjennom `ADMIN_PATH`.
3. Start med Compose som vanlig. Engangscontaineren `data-permissions` retter automatisk eierskapet til datamappen og eksisterende SQLite-filer til UID/GID `10001`, også når et eldre volum er eid av root. Serveren starter først når dette er fullført:

```sh
docker compose up --build -d
```

4. Logg inn, lag en koblingskode per Windows-bruker/enhet og kjør den nye installasjonen. Eksisterende historikk og kvoter migreres automatisk.
5. Kontroller at alle styrte PC-er viser en nylig rapport, og prøv pause/gjenåpning på en testkonto.

`data-permissions` skal stå som `Exited (0)` i `docker compose ps -a`; det er normalt. Den har ikke nettverk og endrer bare eierskap på `/data` og `screengate.db` med eventuelle `-wal`, `-shm` og `-journal`-filer. Andre filer i volumet berøres ikke. Symbolske lenker og uventede filtyper avvises. Selve serveren kjører fortsatt uten root og uten Linux-kapabiliteter. Ved bruk av `docker run` uten Compose må du selv klargjøre eierskapet til datavolumet.

Gamle klienter har ikke den nye autentiseringen eller offline-håndhevingen. En serveroppgradering alene oppgraderer ikke klientene. Endringene er ikke en automatisk utrulling til PC-ene.

Ved bytte av tidssone vil datovisningen av historisk aktivitet kunne endres. Velg riktig tidssone før familien tar systemet i bruk.

## Sikkerhetskopiering

Bruk **Endringslogg og sikkerhetskopi → Last ned sikkerhetskopi** i foreldreoversikten. Serveren lager en konsistent SQLite-kopi, også når databasen er i bruk og har WAL-data. Kopien inneholder personnavn, bruk, regler og hasher av enhetsnøkler. Oppbevar den privat.

For gjenoppretting:

1. Stopp serveren.
2. Behold en kopi av dagens database og eventuelle WAL/SHM-filer før du erstatter noe.
3. Plasser sikkerhetskopien som `DATABASE_PATH`. Gamle WAL/SHM-filer må ikke brukes sammen med en annen database; flytt dem til samme private sikkerhetskopimappe som den gamle databasen.
4. Sørg for at filen eies av serverbrukeren, og start serveren igjen.
5. Kontroller kvoter og enheter. Enheter koblet til etter tidspunktet for sikkerhetskopien må kobles til på nytt.

CSV-eksporten inneholder syv kalenderdager med forbruk; den er ikke en fullstendig sikkerhetskopi. Historikk slettes ikke automatisk i denne utgaven.

## Windows-installasjonen

- Program: `C:\Program Files\ScreenGate\screengate-client.exe`.
- Konfigurasjon: `C:\ProgramData\ScreenGate\<SID>\client.json`.
- Oppgave: `ScreenGate Client <SID>`, ved innlogging for valgt bruker.
- Klienttilstand og logg: `%LOCALAPPDATA%\ScreenGate` i den styrte brukerens profil.

Programfilen deles mellom brukere på samme PC; konfigurasjon, oppgave og lokale tillatelser er separate. Installasjonen stopper kjente ScreenGate-oppgaver mens programfilen byttes. En eksisterende fil erstattes med sikkerhetskopi og forsøkes gjenopprettet ved feil. Hvis automatisk gjenoppretting feiler, beholdes sikkerhetskopien og plasseringen skrives ut.

Installasjonen sjekker SHA-256 fra serveren. Dette oppdager en ufullstendig eller feil nedlasting, men gir ikke beskyttelse mot en angriper som kan endre både filen og sjekksummen på en HTTP-forbindelse. Bruk HTTPS når forbindelsen trenger autentisering av serveren.

`uninstall.ps1 -User "PC\bruker" -WhatIf` viser hva avinstalleringen retter seg mot. Uten `-WhatIf` fjernes bare denne brukerens oppgave og konfigurasjonsfil. Lokale logger og klienttilstand beholdes. Avinstalleringen sletter ikke databasedata på serveren.

## Automatiske klientoppdateringer

Nye installasjoner oppretter den felles Windows-oppgaven `ScreenGate Update`. På eksisterende PC-er aktiveres den én gang med den nye `install.ps1 -UpdatesOnly`, kjørt som administrator. Ny paring er ikke nødvendig, og eksisterende testmodus/låsemodus beholdes. Last ned skriptet fra den oppdaterte serveren først; et gammelt installasjonsskript har ikke denne funksjonen.

Oppgaven kjører skjult som SYSTEM hvert 60. minutt og starter første sjekk etter omtrent to minutter. Den bruker `update.ps1` og `update.json` i `C:\Program Files\ScreenGate`. Vanlige brukere har bare lesetilgang. PC-en trenger nettverkstilgang til serveradressen også fra SYSTEM-kontoen. En sjekk som ble utsatt mens PC-en sov, kjøres når oppgaven igjen kan starte.

Når serverens klientfil endres, laster oppdatereren ned filen og kontrollerer serverens signatur, filstørrelse og SHA-256 før noen klient stoppes. Bare ScreenGate-oppgaver som allerede kjører, startes på nytt. Oppgaven endrer ikke klientkonfigurasjon, paring eller låsemodus. Den forrige programfilen beholdes som `screengate-client.previous.exe`; hvis en omstartet klient ikke blir værende i gang, forsøkes automatisk tilbakeføring. Kontrollen bekrefter oppstart, ikke all funksjonalitet i en ny versjon.

Ved vanlig utrulling er det nok å bygge og starte den nye serverversjonen med tilhørende Windows-klient, for eksempel `docker compose up --build -d`. Ved manuell serverdrift må `CLIENT_BINARY_PATH` peke på den nye klientfilen. Oppdatereren sammenligner filinnhold, så uendret klient blir ikke startet på nytt. Oppdatereren selv er et lokalt installert skript; denne mekanismen oppdaterer klientprogrammet, ikke installasjonsskriptene.

Serverens RSA-nøkkel lagres i databasen og følger sikkerhetskopien. Den offentlige nøkkelen festes til PC-en ved aktivering. Hver sjekk signeres med en ny tilfeldig utfordring, slik at et gammelt svar ikke kan spilles av som en ny oppdatering. HTTPS anbefales ved første aktivering; ved HTTP må installasjonsskriptet og nøkkelen hentes over et nettverk du stoler på. Senere nedlastinger kontrolleres mot nøkkelen som allerede er lagret, også over HTTP. Oppdatereren godtar ikke automatisk en ny nøkkel hvis databasen blir erstattet. Gjenopprett databasen, eller godkjenn en ny server ved å fjerne `update.json` som administrator og kjøre aktiveringen igjen.

Logg: `C:\Program Files\ScreenGate\update.log`. En mislykket sjekk prøves igjen neste time. For umiddelbar sjekk kan en administrator kjøre `Start-ScheduledTask -TaskName 'ScreenGate Update'`. Avinstallering av siste ScreenGate-bruker fjerner også oppdateringsoppgaven. Avinstallering av én bruker lar andre brukeres oppdateringer fortsette.

## Vanlige feil

**Serveren starter ikke:** Kontroller `ADMIN_PASSWORD`, tidssone og skrivetilgang til databasen. `docker compose logs --tail=100` viser oppstartsfeil. `GET /healthz` skal returnere `{"status":"ok"}`.

**`attempt to write a readonly database` etter oppgradering:** Kjør `docker compose up --build -d` med oppdatert Compose-fil, og kontroller `docker compose logs data-permissions`. Ikke bruk `--no-deps`, siden det hopper over klargjøringen av volumet. Ikke slett datavolumet; det inneholder historikk og innstillinger.

**Ingen rapport fra Windows:** Kontroller serveradresse, valgt Windows-bruker, oppgave i Oppgaveplanlegging og brukerens `client.log`. Oppgaven trenger en interaktiv innlogging. En maskin som sover eller har logget ut rapporterer ikke.

**Enheten er koblet til, men skjermen låses:** Sjekk pause, dagens regel, tidsvindu, kvote og nettverk. Tilbakekalt enhetsnøkkel gir også låsing. En enhet uten en gyldig serverbeslutning får ikke en ny offline-kvote.

**Klienten er frakoblet etter navnebytte på PC-en:** Kjør installasjonen med en ny koblingskode. Identiteten som ble gitt ved tilkobling skal brukes uendret i alle rapporter.

**HTTP 403 når et skjema lagres:** Oppdater foreldreoversikten. Serveromstart bytter skjematoken. Kontroller også at proxyen bevarer korrekt vert og opprinnelse.

**HTTP 429:** Vent minst ett minutt før nytt forsøk. Beskyttelsen brukes for innlogging og koblingskoder.

**Uventet låsing etter nettbrudd:** Dette er tilsiktet når den siste tillatelsen på maksimalt 90 sekunder er utløpt. En klient kan hente ny tillatelse mens Windows er låst, og forelderen kan deretter logge brukeren inn igjen.

## Avgrensning for verifisering

Automatiske tester bruker midlertidige databaser, falske klokker og HTTP-testservere. Nettlesertesten starter bare ScreenGate-serveren og bruker en separat database under `.artifacts`. Den installerer ikke klienten og låser ingen Windows-økt.

Før utrulling bør faktisk installasjon, varsler, låsing, opplåsing, søvn og raskt brukerbytte prøves på en egen Windows-standardkonto. Dette kan ikke bekreftes av en kompilering alene.
