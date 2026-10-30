# Steg 6: praktisk kontroll på Windows

**Status: klargjort, ikke praktisk godkjent.** Det mangler valgt test-PC, en interaktiv Windows-standardkonto og observasjoner fra den kontoen. Bygg og automatiske tester bekrefter ikke at Windows faktisk låser, at brukerbytte håndteres riktig eller at en installert klient starter igjen etter oppdatering.

## Klargjort her

- Server og Windows-klient er bygget 29.10.2026 fra revisjon `f721a63` til `.artifacts/step6/screengate.exe` og `.artifacts/step6/screengate-client.exe`. Klientversjon i koden er `2026.10.28.4`. Byggdetaljer og SHA-256 lagres i `.artifacts/step6/build-info.json`.
- `scripts/windows-test-preflight.ps1` viser hvilken konto og økt testen kjøres fra, medlemskap i administratorgruppen, eventuelle klientprosesser og SHA-256 for testfilen. Skriptet leser bare; det starter og stopper ingen klient, endrer ingen oppgave og leser ingen nøkler.
- Den eksisterende oppdateringstesten `scripts/test-updater.ps1` er kjørt og bestod: signatur, filbytte og tilbakeføring i en midlertidig mappe. Nettverk, Oppgaveplanlegging og venting er erstattet i denne testen. Den godkjenner ikke en virkelig installasjon.

Miljøet her kjører som en Codex-sandkassekonto. Det ble også funnet en eksisterende ScreenGate-klient i Windows-økt 2. En installasjon på denne maskinen kan påvirke den: programfil og oppdateringsoppgave deles mellom Windows-brukere, og installasjonsskriptet stopper eksisterende klienter. Ingen installasjon er utført som del av klargjøringen.

## Før vi starter den interaktive testen

Bruk en egen test-PC eller Windows-VM uten en aktiv ScreenGate-installasjon for andre brukere. Oppdateringstesten skal også bruke denne maskinen. Opprett en vanlig standardkonto til testen og behold en egen administratorkonto for installasjon og tilgang etter låsing.

Testserveren skal ha separat database og testbruker. Bruk ingen av barnas kvoter, enhetsnøkler eller oppdateringsoppgaver. Serveren må inneholde endringene fra steg 4 og tilby den bygde testklienten. Først når testmiljøet er valgt, fyller vi inn serveradresse og gjør installasjonen konkret.

Kjør dette fra en vanlig PowerShell i testkontoen, med prosjektet tilgjengelig:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\windows-test-preflight.ps1
```

Hvis testfilen ligger et annet sted, oppgi `-ClientBinary` med filbanen. `AdministratorsGroup` og `Elevated` skal være `false`, og `CodexSandboxAccount` skal være `false`. `UserInteractive=true` er bare en indikasjon; kontoen må faktisk være logget inn på skrivebordet. Resultatet er en forhåndskontroll, ikke en bestått funksjonstest.

Noter PC, Windows-versjon, testkonto, kildekoderevisjon, klientversjon, klientens SHA-256, testserverens adresse og dato før første forsøk. Del aldri enhetsnøkkel eller administratorpassord i testnotatene.

## Gjennomføring: én prøve om gangen

Installer først i testmodus via testserverens vanlige installasjonsfil. Bekreft riktig konto og at loggen viser `test_mode=true`. Bruk en egen ScreenGate-testbruker med kort kvote. Observer både testkontoens `%LOCALAPPDATA%\ScreenGate\client.log` og testserverens `/monitor`.

| Prøve | Gjør dette | Forventet resultat | Status |
| --- | --- | --- | --- |
| Testmodus | Bruk opp en kort kvote. | Loggen viser `would_lock=true`; klienten låser ikke Windows. | Ikke kjørt |
| Vanlig bruk | Gi ny kvote, bruk PC-en ulåst i to minutter, også uten tastatur/mus. | Tiden trekkes mens økten er ulåst. Serveren bekrefter rapportert forbruk. | Ikke kjørt |
| Låsing og opplåsing | Win+L, vent to minutter og lås opp manuelt. | Loggen skifter til `locked`, så `active`. Låst tid belastes ikke; siste ulåste intervall kan vises i en forsinket rapport. | Ikke kjørt |
| Nettbrudd med restkvote | Ha minst fem minutter igjen. Koble fra nettet i to minutter mens PC-en er ulåst. | Ingen låsing bare på grunn av 90 sekunder uten svar. Lokal restkvote faller med målt bruk. | Ikke kjørt |
| Gjenoppkobling | Koble til nettet etter forrige prøve. | Forbruket avstemmes uten påfyll eller dobbelt trekk; serveren overtar saldoen. | Ikke kjørt |
| Søvn | Noter saldo. Sett PC-en i søvn i minst to minutter og vekk den. | Søvnperioden belastes ikke som målt ulåst bruk. Nye rapporter kommer etter oppvåkning. | Ikke kjørt |
| Raskt brukerbytte | Bytt fra testkontoen til en annen konto, vent to minutter og bytt tilbake. | Testkontoens forbruk står stille mens den er frakoblet. Andre kontoer blir ikke låst av testklienten. | Ikke kjørt |
| Faktisk låsing | På testmaskinen: installer i låsemodus, gi kort kvote og bruk den opp. | Windows låser faktisk. Ny opplåsing mens kvoten er oppbrukt fører til nytt låseforsøk. Administratorkontoen gir vei tilbake. | Ikke kjørt |
| Oppbrukt kvote uten nett | Gi ny kort kvote, bekreft mottatt saldo og koble fra nettet før kvoten er brukt opp. | Klienten låser når lokal restkvote er brukt opp, også uten kontakt. | Ikke kjørt |
| Omstart uten nett | Med gyldig kvote og målt offline-bruk: start klienten på nytt i samme testkonto. | Brukt tid kommer ikke tilbake. Ingen søvn-/stoppetid etterbelastes. | Ikke kjørt |
| Oppdatering | Installer en avtalt eldre testfil, tilby testbygget på testserveren og start den ekte oppdateringsoppgaven på testmaskinen. | Ny SHA-256 installeres, klientoppgaven kommer tilbake og paring/modus/saldo beholdes. `/monitor` får nye rapporter med forventet versjon. | Ikke kjørt |

Ved prøven med faktisk låsing må en ny tillatelse gis gjennom testserveren for videre bruk. Windows låses opp manuelt; et serversvar låser ikke opp skjermen. Ikke tilbakekall nøkkelen som vanlig avslutning av en klient i låsemodus: nøkkelavvisning er en låsegrunn.

For oppdateringsprøven må den eldre og den nye filen ha forskjellig innhold og kjente SHA-256-verdier. Samme fil to ganger skal ikke føre til omstart. En ny versjonstekst alene er ikke bevis på at filbyttet, oppgavestarten og bevart paring faktisk virker.

## Bevis og avslutning

For hver prøve: noter faktisk handling, tid før/etter, saldo før/etter, relevant loggutdrag, dashboardets mottakstid og resultatet sett på skjermen. Merk «ikke kjørt» eller «feilet» der dette gjelder. Hvis en Windows-avlesingsfeil oppstår naturlig, noter kall, tidspunkt og tilgjengelig kode samt eventuell gjenoppretting; en slik feil er ikke påvist bare fordi diagnostikkoden finnes.

Stopp testklienten eller avinstaller den fra testmaskinen før testserveren fjernes. Behold testnotatene uten nøkler. Steg 6 kan først markeres ferdig når de interaktive prøvene er gjennomført og eventuelle feil er fulgt opp. Utrulling til barnas PC-er er en senere handling.
