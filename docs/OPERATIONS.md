# Drift, oppgradering og feilsøking

## Innstillinger

| Miljøvariabel | Standard | Bruk |
| --- | --- | --- |
| `ADMIN_PASSWORD` | Ingen | Påkrevd, minst 12 tegn |
| `ADMIN_USER` | `admin` | Brukernavn for foreldreoversikten |
| `ADMIN_PATH` | `/admin` | Én sti-komponent, for eksempel `/familie`; erstatter ikke innlogging |
| `DATABASE_PATH` | `screengate.db` | Databasefil; Compose bruker `/data/screengate.db` |
| `SCREENGATE_TIMEZONE` | `Europe/Oslo` | Tidssone for kalenderdager og ukeplan |
| `LISTEN_ADDR` | `:8080` | Lokal serveradresse |
| `CLIENT_BINARY_PATH` | `/client/screengate-client.exe` eller lokal fil | Nedlastbar Windows-klient |
| `SCREENGATE_BIND` | `127.0.0.1` | Compose: hvilken vertsadresse som eksponeres |
| `SCREENGATE_PORT` | `8081` | Compose: port på verten |

`.env` leses av Docker Compose. `go run .` bruker miljøvariablene fra skallet og leser ikke filen automatisk. Den lokale `.env`-filen og databaser er ignorert av Git og Docker-bygget.

Foreldreoversikten bruker nettleserens HTTP Basic-dialog. Velg en egen lang passordfrase. HTTPS håndteres normalt av en reverse proxy. Proxyen må videresende opprinnelig `Host`, `Authorization` og `Origin`; serverens kontroll av forespørsler fra andre nettsider skal ikke skrus av. Innloggingsforsøk begrenses etter direkte klient-IP; bak en proxy kan flere klienter dele denne grensen.

## Oppgradering fra første ScreenGate-versjon

1. Stopp den gamle serveren og ta en kopi av hele det vedvarende datavolumet. Ved filbasert kopi må serveren være stoppet; SQLite kan ha nyere data i `-wal`-filen.
2. Lag `.env` med eget administratorpassord. Den gamle skjulte adminstien kan beholdes gjennom `ADMIN_PATH`.
3. Den nye containeren kjører som UID/GID `10001`. Hvis det eksisterende volumet er eid av root, endre eierskapet til datamappen og SQLite-filene før normal oppstart:

```sh
docker compose build
docker compose run --rm --no-deps --user 0 --entrypoint sh screengate -c 'for p in /data /data/screengate.db /data/screengate.db-wal /data/screengate.db-shm; do if [ -e "$p" ]; then chown 10001:10001 "$p"; fi; done'
docker compose up -d
```

4. Logg inn, lag en koblingskode per Windows-bruker/enhet og kjør den nye installasjonen. Eksisterende historikk og kvoter migreres automatisk.
5. Kontroller at alle styrte PC-er viser en nylig rapport, og prøv pause/gjenåpning på en testkonto.

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

## Vanlige feil

**Serveren starter ikke:** Kontroller `ADMIN_PASSWORD`, tidssone og skrivetilgang til databasen. `docker compose logs --tail=100` viser oppstartsfeil. `GET /healthz` skal returnere `{"status":"ok"}`.

**Ingen rapport fra Windows:** Kontroller serveradresse, valgt Windows-bruker, oppgave i Oppgaveplanlegging og brukerens `client.log`. Oppgaven trenger en interaktiv innlogging. En maskin som sover eller har logget ut rapporterer ikke.

**Enheten er koblet til, men skjermen låses:** Sjekk pause, dagens regel, tidsvindu, kvote og nettverk. Tilbakekalt enhetsnøkkel gir også låsing. En enhet uten en gyldig serverbeslutning får ikke en ny offline-kvote.

**Klienten er frakoblet etter navnebytte på PC-en:** Kjør installasjonen med en ny koblingskode. Identiteten som ble gitt ved tilkobling skal brukes uendret i alle rapporter.

**HTTP 403 når et skjema lagres:** Oppdater foreldreoversikten. Serveromstart bytter skjematoken. Kontroller også at proxyen bevarer korrekt vert og opprinnelse.

**HTTP 429:** Vent minst ett minutt før nytt forsøk. Beskyttelsen brukes for innlogging og koblingskoder.

**Uventet låsing etter nettbrudd:** Dette er tilsiktet når den siste tillatelsen på maksimalt 90 sekunder er utløpt. En klient kan hente ny tillatelse mens Windows er låst, og forelderen kan deretter logge brukeren inn igjen.

## Avgrensning for verifisering

Automatiske tester bruker midlertidige databaser, falske klokker og HTTP-testservere. Nettlesertesten starter bare ScreenGate-serveren og bruker en separat database under `.artifacts`. Den installerer ikke klienten og låser ingen Windows-økt.

Før utrulling bør faktisk installasjon, varsler, låsing, opplåsing, søvn og raskt brukerbytte prøves på en egen Windows-standardkonto. Dette kan ikke bekreftes av en kompilering alene.
