# Steg 2: dagens klientflyt gjengitt i tester

Status: **fullført**. Grunnlag: [tilstandsrapporten](STATE_REPORT.md) og [vedtatte regler](BEHAVIOR_RULES.md). Dette steget dokumenterer dagens oppførsel; det innfører ikke de nye reglene.

## Endring gjort for å kunne teste

Hendelsesløkken er flyttet fra `cmd/client/main_windows.go` til `cmd/client/runtime.go`, som `runClient`. Windows-oppstart, flagg, konfigurasjon og valg av testmodus ligger fortsatt i Windows-filen.

Klokke, tikk fra tidsur, HTTP-klient, lagring og Windows-handlinger sendes inn ved oppstart av løkken. I vanlig kjøring brukes de samme funksjonene som før. Testene kjører den samme løkken med styrt tid og simulerte nettverkssvar og Windows-handlinger. Den uttrukne løkken er sammenlignet med originalen: etter erstatning av disse ytre kallene er selve løkkekroppen uendret.

Testene bruker den faktiske HTTP-protokollkoden, tilstandsberegningen, signeringen og lagringen i midlertidige filer. Nettverket simuleres gjennom HTTP-klientens transport. Klokke og hendelser styres uten venting på reell spilletid. Låsing og varsler registreres som testhandlinger og utfører ingen Windows-handling.

## Tester og avvik fra vedtatte regler

Alle nye tester ligger i `cmd/client/runtime_test.go`. Navnene begynner med `TestCurrentLoop` for å vise at de beskriver dagens oppførsel, inkludert kjente feil. Grønne tester betyr at denne oppførselen er gjengitt; de betyr ikke at vedtatte regler er implementert.

| Situasjon | Bekreftet i dagens løkke | Forhold til vedtatt regel | Test |
| --- | --- | --- | --- |
| Kontaktfeil ved ny klient, etter tillatelse og etter låsebeslutning | Nettfeil, HTTP 503 og ugyldig svar åpner tilgangen. HTTP 401/403 stenger bare når `ServerContacted` allerede er sann. | Avvik: manglende lagrede regler skal føre til stenging, og kontaktfeil skal ikke oppheve aktiv stenging. | `TestCurrentLoopContactFailures` |
| Avvist nøkkel etter kontaktfeil | Handlingen settes til `lock`, men tilgangskontrollen tillater fortsatt bruk. Ingen låsehandling utføres. | Avvik: HTTP 401/403 skal stenge uansett tidligere kontaktfeil. | `TestCurrentLoopAuthenticationFailureAfterOutageStillAllows` |
| Lokal kvote brukes opp uten kontakt | Lokal resttid når null, men tilgangen forblir åpen og løkken kaller ikke låsing. | Avvik: siste restkvote skal brukes opp lokalt og deretter stenge. | `TestCurrentLoopOfflineExhaustionDoesNotLock` |
| Kontakt tilbake etter lokal bruk | Klienten prøver rapporten igjen uten å sende aktivitetssekundene. Med 100 sekunder ved start og 10 brukt lokalt kan et eldre serversvar sette lokal resttid til 95 i stedet for 90. | Avvik: klientens oppdaterte restkvote må avstemmes før serveren overtar som master. | `TestCurrentLoopReconnectCanReturnLocallyUsedTime` |
| Omstart under kontaktfeil | Restkvoten bevares. Innlasting endrer likevel feiltilstandens flagg, slik at klienten forsøker låsing mens første forespørsel venter. En ny kontaktfeil åpner tilgangen igjen. | Avvik: lagret restkvote og regler skal videreføres uten at omstart eller en ny nettfeil skifter tilgangsregel. | `TestCurrentLoopRestartChangesOutageEnforcement` |
| Nettverkssvar venter mens lokal kvote brukes opp | Lokale kontroller og eventuell låsing fortsetter. Testmodus teller videre uten å utføre låsing. | En eksisterende egenskap vi skal beholde. Den gjelder her før en feil har satt løkken i tilstanden som åpner tilgang. | `TestCurrentLoopLocalEnforcementContinuesDuringPendingRequest` |
| Windows-adapteren returnerer sin eksisterende `locked`-fallback | Videre opptelling og låseforsøk undertrykkes, selv med en eksplisitt avvisning fra serveren. | Bekrefter konsekvensen av dagens fallback. Vedtatt tiltak er først observasjon; ingen ny feilregel er innført. | `TestCurrentLoopLockedFallbackStopsCountingAndSuppressesLock` |
| Øktstatus endres mens en rapport venter | Endringen rapporteres etter svaret, med ny rapport-ID. En serverpause ved neste opplåsing utløser låseforsøk. | En eksisterende egenskap vi skal beholde. | `TestCurrentLoopSendsChangedSessionAfterPendingResponse` |
| Ulåst bruk, varsel, låsing og et langt opphold | Lokal tid reduseres under ulåst bruk, varselgrensen utløses, låst tid og oppholdet belastes ikke. En gammel tillatelse kan likevel utløpe og føre til låseforsøk etter oppholdet. | Telling og varsler har et testgrunnlag. Tillatelsens utløp må senere samordnes med reglene om lokal restkvote og lagrede regler. | `TestCurrentLoopCountingWarningsAndSuspension` |

## Hva testene ikke bekrefter

- Ingen faktisk Windows-økt eller fysisk nettverksforbindelse er undersøkt. Testene beviser ikke at avlesingsfeil har forekommet på barnas PC-er.
- Testen av avlesingsfeil mater inn adapterens eksisterende `locked`-resultat og bruker den eksisterende WTS-parseren med manglende data. Den viser hva løkken gjør med dette resultatet; den injiserer ikke feil inne i Windows-API-ene.
- Ny avlesingsdiagnostikk er ikke implementert. Det er en senere endring etter valget om observasjon først.
- Ny lokal ukeplan, døgnskifte uten nett og avstemming av flere frakoblede enheter er ikke implementert. Eksisterende policy- og regnskapstester inngår fortsatt i testsuiten; de dokumenterer dagens server, ikke disse fremtidige klientfunksjonene.

## Verifisering

Disse kontrollene bestod:

- `go test ./...` før uttrekkingen.
- `go test ./cmd/client -run TestCurrentLoop -count=1 -timeout=90s` under arbeidet.
- `go test -race ./...` etter at alle nye tester var lagt til.
- `go vet ./...`.
- Windows-bygg: `go build -ldflags "-H=windowsgui" -o .artifacts/screengate-client-step2.exe ./cmd/client`.

Testbygget er ikke installert eller startet. Klientversjon og utrulling er ikke endret.

## Neste avgrensede arbeid

Steg 3 er videre opprydding av ansvar, med disse testene som grunnlag. Kjente feil skal ikke rettes tilfeldig under flytting av kode. Når en vedtatt regel implementeres senere, skal testen som beskriver det gamle avviket endres til å kontrollere den nye oppførselen, sammen med dokumentasjonen.

Steg 3 er ikke startet gjennom denne oppgaven.
