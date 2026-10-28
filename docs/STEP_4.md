# Steg 4, del 1: behold restkvoten ved kontaktfeil

Klientversjon: **2026.10.28.1**. Denne delen innfører den første av de [vedtatte reglene](BEHAVIOR_RULES.md). Full offline-avstemming og lagret ukeplan er fortsatt egne oppgaver.

## Oppførselen som er endret

| Situasjon | Tidligere | Nå |
| --- | --- | --- |
| Kontaktfeil med restkvote igjen | Feilen åpnet tilgang uten å håndheve restkvoten. | Siste beslutning beholdes. Klienten trekker ulåst bruk lokalt og forsøker å låse når restkvoten er brukt opp. |
| Mer enn 90 sekunder siden siste svar | Utløp kunne stenge tilgang med restkvote, mens kontaktfeil kunne åpne den igjen. | Den korte tillatelsesfristen stenger ikke alene. Lokal restkvote og sist kjente stengetid styrer tilgang. |
| Kontaktfeil etter pause eller annen låsebeslutning | Feilen kunne oppheve stengingen. | Stengingen beholdes. |
| Omstart etter kontaktfeil | En lagret feiltilstand kunne låse til en ny nettfeil åpnet tilgang igjen. | Gyldig restkvote, beslutning og forbruk videreføres uten påfyll. Søvn og omstart belastes ikke som målt ulåst bruk. |
| Ingen tidligere gyldig serverbeslutning | Klienten tillot bruk mens den forsøkte å få kontakt. | Tilgang holdes stengt. Kontakt forsøkes ved oppstart, ved gjenopplåsing og gjennom vanlig rapportering. |
| HTTP 401/403 | Kunne ignoreres etter kontaktfeil eller ved første oppstart. | Nøkkelavvisning stenger tilgang også i disse tilfellene. |
| Testmodus | Ingen Windows-låsing. | Samme regelvurdering og lokale telling, fortsatt ingen Windows-låsing. |

Håndheving forutsetter fortsatt at klienten kjører og leser Windows-status riktig. Den særskilte, vedtatte observasjonen av avlesingsfeil er ikke innført i denne delen.

## Hvordan koden uttrykker regelen

`ServerContacted` betyr at klienten har mottatt en gyldig beslutning. Det nye lagrede feltet `ServerUnavailable` betyr at siste kontaktforsøk feilet. Kontaktfeil endrer dermed ikke historikken eller tillatelsen. Et gyldig svar nullstiller feilflagget.

`markServerUnavailable` setter bare feilflagget. Den endrer ikke handling, årsak, kvote eller stengetid. `allowed` kontrollerer tidligere beslutning, klokketilbakegang, handling, restkvote og kjent stengetid. Den bruker ikke lenger `LeaseExpiresAt` som en egen tilgangsgrense.

Det gamle lease-feltet og det eksisterende svaret med `lease_seconds` beholdes foreløpig av hensyn til eldre filformat og klienter. Ingen serverrute eller databasetabell er endret.

## Gamle lagrede feiltilstander

Den gamle klienten kunne lagre `reason=server_unavailable` og `action=allow` etter å ha slettet siste beslutning. Det er da ikke mulig å vite om serveren opprinnelig hadde tillatt bruk eller sagt lås.

Den nye klienten bevarer tallene i filen, men krever en ny serverbeslutning før slike tvetydige filer tillater bruk. Å oppgradere en PC som sist lagret denne tilstanden mens serveren fortsatt er utilgjengelig kan derfor føre til låsing. Vanlige, gyldige filer med en bevart beslutning og restkvote videreføres.

## Tester og kontroll

Testene fra steg 2 som beskrev den gamle feiloppførselen er oppdatert til de vedtatte utfallene. Tester i `offline_test.go` kontrollerer blant annet:

- 100 sekunder lokal bruk uten serverkontakt etterlater 200 av 300 sekunder, uten låsing ved 90-sekundersgrensen.
- En kjent stengetid beholdes ved kontaktfeil, selv med restkvote igjen.
- Ingen lagrede regler gir stenging og nye kontaktforsøk ved gjenopplåsing.
- Oppbrukt offline-kvote vurderes også i testmodus uten å låse Windows.
- En gammel fil som har mistet siste beslutning får ikke fri tilgang ved en ny kontaktfeil.

Omstartstesten kontrollerer også at et opphold på én time ikke nullstiller eller etterbelaster restkvoten. Kompatibilitets-, signerings- og lagringstestene inngår fortsatt i testsuiten.

Kontrollene er `go test ./...`, `go test -race ./...`, `go vet ./...` og Windows-bygg til `.artifacts/screengate-client-step4-offline.exe`. Klienten er ikke installert eller startet som del av arbeidet. Faktisk Windows-låsing må fortsatt prøves på en egen standardkonto før utrulling.

## Hva som gjenstår

**Gjenoppkobling er ennå ikke avstemt.** Serveren bruker fortsatt mottatte rapportintervaller, og klienten sender ikke oppdatert restkvote eller sitt samlede offline-forbruk. Et eldre serversvar kan derfor fortsatt gi tilbake lokalt brukt tid. Den eksisterende testen `TestCurrentLoopReconnectCanReturnLocallyUsedTime` dokumenterer dette gjenværende avviket. Neste del skal løse avstemmingen før serveren overtar som master igjen.

Full lagret ukeplan, ny dagskvote ved midnatt uten nett og lokal håndtering av fremtidige åpningstider gjenstår også. Denne delen bevarer allerede kjent kvote og stengetid; den er ikke en komplett implementering av alle offline-reglene.
