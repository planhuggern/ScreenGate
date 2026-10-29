# Steg 4: lokal kvote og avstemming

Del 1 ble innført i klientversjon **2026.10.28.1**. Del 2, avstemming ved gjenoppkobling, er innført i **2026.10.28.2**. Del 3, lagret ukeplan og ny dagskvote uten nett, er innført i **2026.10.28.3**. Del 4, observasjon av Windows-avlesingsfeil, er innført i **2026.10.28.4**. Del 2–4 har tilhørende serveroppdateringer. Delene følger de [vedtatte reglene](BEHAVIOR_RULES.md).

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

## Del 2: serveren overtar etter avstemming

Klienten måler bruksintervaller lokalt og sender dem sammen med restkvoten i en uforanderlig rapport om forbruk. Rapporten har en egen ID. Den beholdes i tilstandsfilen til serveren har bekreftet akkurat denne ID-en. En ny heartbeat-ID ved låsing eller opplåsing endrer ikke ID-en for utestående forbruk.

1. Klienten sender et øyeblikksbilde av restkvoten og de målte intervallene, inntil 32 intervaller per rapport.
2. Serveren lagrer forbruket og bekreftelsen i samme transaksjon som heartbeat og enhetsstatus. Samme rapport belastes bare én gang, også om den kommer med en ny heartbeat-ID.
3. Serveren vurderer gjeldende kvote, pause, bonus og tidsplan på nytt. Forbruk fra andre PC-er beholdes; en klients resttall overskriver ikke fellesregnskapet.
4. Svaret inneholder `usage_ack`. Først når denne samsvarer med den sendte rapporten, godtar klienten den nye serverbeslutningen og fjerner det bekreftede forbruket fra køen.
5. Bruk registrert mens svaret var på vei trekkes fortsatt lokalt. Ved gjenoppkobling sender klienten resten av den oppsamlede køen videre uten å vente på neste ordinære 30-sekundersrapport.

Eksempel: 25 minutter igjen ved nettbrudd, 10 minutter målt bruk, deretter kontakt tilbake gir 15 minutter igjen. Et mistet svar eller gjentatt sending gir fortsatt 15 minutter, ikke 25 eller 5. Hvis to PC-er har brukt 10 og 5 minutter av samme sist kjente restkvote på 30 minutter, blir den felles restkvoten 15 minutter etter avstemming.

For nye klienter bruker serveren de målte intervallene både under normal kontakt og ved gjenoppkobling. Den legger ikke også til forbruk fra mottakstidspunktene. Eldre klienter fortsetter med den gamle serverberegningen, inkludert 63-sekundersgrensen. Dette gjør overgangen gradvis uten dobbeltelling.

### Datoer, lagring og oppgradering

Nye intervaller er tidsstemplet i UTC, med klokkejustering fra serverens tid. Dagsgrenser og timesøyler beregnes etter serverens tidssone. Forsinket forbruk havner dermed på datoen det ble registrert, også ved midnatt og sommer-/vintertid. En bekreftet ny dagskvote reduseres bare av utestående bruk fra den samme dagen.

Tilstandsfilen beholder både rapporten som venter på bekreftelse og nyere, ikke sendte intervaller ved omstart. Sammenhengende bruk samles i intervaller på høyst én time. Den lokale filgrensen er økt til 1 MiB for køen; en for stor fil erstatter ikke siste lesbare tilstandsfil. Nettverksrapportene er fortsatt begrenset til 32 intervaller og serverens eksisterende grense på 16 KiB.

Ved første oppstart fra et eldre filformat finnes det ingen nøyaktige tidsstempler for gammelt offline-forbruk. Klienten sender da én overgangsrapport med gammel dagskvote og restkvote. Serveren sikrer at allerede kjent dagsforbruk minst tilsvarer dette, uten å belaste samme kjente forbruk på nytt. Nyere forbruk registreres deretter med ordinære tidsstempler.

Overgangen kan ikke rekonstruere den nøyaktige timefordelingen eller summere alt samtidig, uregistrert offline-forbruk fra flere gamle klienter. Den setter et minste kjent dagsforbruk, og den gamle tilleggstiden plasseres samlet innenfor den aktuelle dagen. Gamle ubegrensede kvoter gir ikke et resttall som rekonstruerer tidligere bruk. Dette er en begrensning i de gamle dataene, ikke i avstemmingen av nye intervaller.

Serveroppdateringen må være på plass før klient `2026.10.28.2` tas i bruk. Den krever en eksplisitt bekreftelse som eldre servere ikke gir. Uten en gyldig bekreftelse bevares lokalt forbruk og siste beslutning, fremfor å godta et eldre serversvar som gir tiden tilbake. Den nye serveren støtter fortsatt eldre klienter.

### Koden og kontrollene for del 2

| Fil | Ansvar |
| --- | --- |
| `cmd/client/usage.go` | Lokal intervallkø, stabile rapport-ID-er, klokkejustering og beregning av utestående bruk per dato. |
| `cmd/client/state.go` | Oppdatere lokal restkvote og ta imot bekreftede serverbeslutninger. |
| `cmd/client/protocol.go` | Sende rapporten og kreve riktig `usage_ack`. |
| `usage_reconciliation.go` | Kontrollere rapporten og lagre forbruk én gang i servertransaksjonen. |
| `migrations/00007_usage_reconciliation.sql` | Tilføye serverens bekreftelser og målte intervaller. |

Testene dekker tapte svar, endret låsestatus, omstart, flere PC-er, endret bonus/pause, feil bekreftelse, gammel restkvote, midnatt, sommer-/vintertid, parallelle rapporter, oppgradering av eksisterende database og tilbakerulling ved skrivefeil. Testen som tidligere viste at brukt tid kom tilbake er erstattet med `TestLoopReconnectReconcilesBeforeAcceptingServerQuota`.

Del 2 kontrolleres med `go test ./...`, `go test -race ./...`, `go vet ./...` og separate server-/klientbygg under `.artifacts`. Ingen klient er installert eller startet, og ingen utrulling er gjort.

## Del 3: lagret ukeplan og døgnskifte uten nett

Serveren sender nå et `policy`-objekt med standardkvote, alle ukedager, pause og dagens bonus med dato. Klienten lagrer dette i den signerte tilstandsfilen sammen med restkvoten. Det er ingen nye innstillinger eller databaseendringer i denne delen.

Ved nettbrudd gjelder følgende:

- Dagens restkvote videreføres og trekkes lokalt. Klienten beregner ikke dagens restkvote på nytt fra ukeplanen.
- Ved midnatt etter serverens tidssone starter den nye ukedagens kvote. Gårsdagens bonus følger ikke med. Omstart samme dag fyller ikke opp kvoten igjen.
- Lagrede tidsvinduer kan både stenge og åpne tilgang. En uttrykkelig stengt dag overstyrer også et tidsvindu fra kvelden før. Ubegrenset kvote opphever ikke tidsplan eller pause.
- En pause fortsetter til et nytt serversvar opphever den. Nøkkelavvisning, lagringsfeil og oppdaget klokketilbakegang kan heller ikke oppheves av den lagrede ukeplanen.
- Søvn og avslått PC telles ikke som bruk. Hvis flere dager er passert, brukes reglene for datoen klienten kommer tilbake på.

Eksempel: Onsdag har barnet 20 sekunder igjen, inkludert eventuell bonus. Torsdagens kvote er 120 sekunder. Ti sekunder ulåst bruk på hver side av midnatt gir 110 sekunder igjen torsdag. Rapporten til serveren beholder ti sekunder på hver dato. En omstart torsdag gir fortsatt 110 sekunder, ikke 120.

En liten lokal oversikt over eget forbruk per dato beskytter også mot et serversvar som var på vei over midnatt. Den beholder både bekreftet og ubekreftet bruk. Ved dagens ordinære avstemming er serverens felles restkvote fortsatt autoritativ, også når andre PC-er har brukt tid. Gamle datoer ryddes bort automatisk.

### Hvor reglene ligger

| Fil | Hva du leser den for |
| --- | --- |
| `internal/screenpolicy/rules.go` | Felles forståelse av ukedager, tidsvinduer, midnatt og datobundet bonus. Server og klient bruker samme kode. |
| `cmd/client/local_policy.go` | Når dagens kvote videreføres, når en ny dag starter og hvordan målt bruk fordeles mellom datoene. |
| `cmd/client/state.go` | Rekkefølgen ved lokal telling og når en bekreftet serverbeslutning erstatter tilstanden. |
| `cmd/client/local_policy_test.go` | Konkrete eksempler for midnatt, omstart, åpning/stenging, pause og sommer-/vintertid. |

Kalenderen følger `timezone` fra serveren, normalt `Europe/Oslo`, uavhengig av PC-ens tidssone. Bruk en navngitt tidssone i serverinnstillingen `SCREENGATE_TIMEZONE` for korrekte fremtidige sommertidsendringer. Den eldre verdien `Local` kan bare videreføres med sist mottatte UTC-avvik.

### Oppgradering og kontroll

Oppdater serveren før klient **2026.10.28.3** for å få ukeplanen overført. Eldre klientfiler er fortsatt lesbare, men inneholder ingen full ukeplan. Før første gyldige svar med `policy` viderefører klienten den gamle restkvoten og kjente stengetiden; den finner ikke på fremtidige dagskvoter. Et svar fra en eldre server uten `policy` erstatter også den gamle planen, slik at en nyere pausebeslutning ikke overstyres lokalt.

Testene dekker døgnskifte med ulike ukedagskvoter, bonusutløp, forbruk på begge sider av midnatt, omstart uten påfyll, pause, stengt dag, ubegrenset kvote, søvn over flere dager, åpning etter et stengt tidsvindu, gjentatte og manglende sommertidstimer, serverens tidssone, avstemming med andre enheters forbruk, forsinket svar over midnatt og ugyldige regeldata. En test kjører midnatt gjennom den vanlige klientløkken med utilgjengelig server.

Kontrollene er `go test ./...`, `go test -race ./...`, `go vet ./...` og separate Windows-bygg under `.artifacts`. Ingen klient er installert eller startet, og ingen utrulling er gjort.

## Del 4: synlige feil ved avlesing av Windows-status

Klienten skiller nå en bekreftet øktstatus fra reserveverdien som brukes når Windows-avlesingen feiler. Dette er observasjonstiltaket vi avtalte. Feilen innfører ingen ny telle- eller låseregel og ingen 30-sekundersfrist.

| Situasjon | Fortsatt brukt til telling/låsing | Ny diagnostikk |
| --- | --- | --- |
| Feil fra `ProcessIdToSessionId`, `WTSQuerySessionInformationW`, `OpenInputDesktop` eller `GetUserObjectInformationW` | `locked` som før | Kall, tidspunkt og eventuell tilgjengelig Windows-feilkode. |
| Ufullstendig WTS-svar, ukjent format/flagg/tilstand eller feil økt i svaret | `locked` som før | Konkret tolkningsfeil uten oppdiktet feilkode. |
| `GetLastInputInfo` feiler | `active` som før | Feilet kall uten feilkode; denne funksjonen dokumenterer ingen utvidet feilkode. |
| Gyldig låseflagg, frakoblet økt eller kjent annet skrivebord enn `Default` | `locked` som før | Vellykket avlesing av den eksisterende tilstandsvurderingen. |

Microsoft beskriver bare returverdien for [GetLastInputInfo](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-getlastinputinfo). Klienten viderefører derfor ikke en mulig gammel trådfeilkode fra dette kallet. De øvrige feilene beholder tilgjengelige koder fra det feilede kallet; vellykkede kall gir ingen feilkode.

### Hva du ser

Klientloggen `%LOCALAPPDATA%\ScreenGate\client.log` registrerer feil med tidspunkt, kall, årsak, eventuell kode og brukt reserveverdi. Første feil og endret feil logges straks. Ved samme vedvarende feil kommer en påminnelse hvert minutt. Når avlesingen fungerer igjen, logges gjenopprettingen. Dette skjer også uten nett.

Heartbeats får et valgfritt `session_diagnostics`-objekt. Endret feiltilstand eller gjenoppretting forsøkes rapportert straks, også når reserveverdien ikke endres. Hvis en forespørsel allerede venter, sendes siste diagnostikk etterpå. Siste feil og siste vellykkede avlesing lagres med klienttilstanden og overlever omstart.

Dashboardet viser en egen kolonne for Windows-avlesing og fremhever en pågående feil. Øktstatus merkes da «Ukjent · reserveverdi: …». Sist vellykkede avlesing vises som tidsstemplet historikk, ikke som bekreftelse på nåværende Windows-status. Etter gjenoppretting vises «Avlesing OK» og tidspunktet for siste feil. Manglende nettverksrapporter og avviste forespørsler vises fortsatt separat. Tidspunkter for avlesing kommer fra PC-ens klokke; mottakstidspunktet kommer fra serveren.

Klienten beholder siste feil, ikke en ubegrenset kø av alle feilperioder. Flere feil under samme nettbrudd finnes i den lokale loggen; dashboardet får den siste når kontakt gjenopprettes. Dashboardets historikk er fortsatt de 100 siste mottatte heartbeats. Det kan fortsatt ikke fastslå at en klient har krasjet eller er avsluttet med vilje.

### Hvor koden ligger

| Fil | Ansvar |
| --- | --- |
| `cmd/client/session_windows.go` | De faktiske Windows-kallene og tilgjengelige feilkoder. |
| `cmd/client/session_reader.go` | Leserekkefølgen og de eksisterende reserveverdiene, med feil vedlagt. |
| `cmd/client/session_diagnostics.go` | Logging, gjenoppretting og historikk for siste feil/vellykkede avlesing. |
| `internal/sessiondiag/report.go` | Det felles, begrensede rapportformatet for klient og server. |
| `heartbeat_monitor*.go` | Kontroll, lagring og visning av diagnostikken på serveren. |
| `migrations/00008_session_diagnostics.sql` | Nye diagnostikkfelt i overvåkingens to tabeller. |

Serveren lagrer bare kjente kall og årsakskoder, numeriske feilkoder og tidsstempler fra denne diagnostikken. Vilkårlig klienttekst vises ikke på den offentlige siden. Gyldig kvotebeslutning og avstemming avhenger ikke av om diagnostikken kan brukes. Eldre klienter viser «Ikke rapportert av klienten». Eldre klientfiler kan fortsatt leses.

Oppdater serveren før klient **2026.10.28.4** for å se feilene i dashboardet. Klienten logger lokalt også før serveren støtter feltet. Testene bruker simulerte Windows-resultater og den vanlige klientløkken; de fremprovoserer ingen faktisk Windows-feil og låser ingen økt. De dekker reserveverdier, fraværende feilkode, gjentatt feil, gjenoppretting, nettbrudd/omstart, feil mens en rapport venter, offentlig visning, skjerming av klienttekst, eldre klienter og bevaring av gammel overvåkingshistorikk gjennom migreringen.

Kontrollene er `go test ./...`, `go test -race ./...`, `go vet ./...` og server-/klientbygg under `.artifacts`. Ingen installasjon eller utrulling er gjort.

## Hva som gjenstår

De avtalte kodeendringene i steg 4 er gjennomført. Dagens reserveverdier ved Windows-feil har fortsatt de kjente konsekvensene for telling og låsing; eventuell endring krever et nytt regelvalg basert på observasjoner. Oppryddingen er gjennomført i [steg 5](STEP_5.md). Praktisk Windows-kontroll i steg 6 gjenstår: faktisk låsing og oppdatering må prøves på en egen Windows-standardkonto før utrulling. Beskyttelse mot at klientprosessen avsluttes er fortsatt et eget senere prosjekt.
