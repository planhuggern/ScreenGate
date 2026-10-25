# Tilstandsrapport og plan for regler

Dato: 24. oktober 2026. Grunnlag: kildekoden ved commit `faad09e`, eksisterende tester og dokumentasjon. Ingen programkode eller eksisterende regler er endret i denne gjennomgangen. Forslagene nedenfor er ikke vedtatte regler.

## Vurdering

ScreenGate har en brukbar kjerne for tidsberegning og ukeregler. Hovedproblemet er at klienten, dokumentasjonen og deler av testene beskriver forskjellige modeller for tilgang ved feil. Flere rester fra tidligere modeller gjør det vanskelig å se hva som faktisk brukes.

Vi bør avklare oppførselen ved nettbrudd og ukjent Windows-status før vi refaktorerer håndhevingen. Å bare flytte funksjoner til nye filer vil ikke løse disse motsetningene. En full omskriving er heller ikke begrunnet av denne gjennomgangen.

Dette er en vurdering av kodebasen. Den fastslår ikke årsaken til hendelser på barnas PC-er, og bekrefter ikke hvilken installasjon eller låsemodus hver PC faktisk bruker.

## Hva systemet gjør i dag

| Område | Faktisk oppførsel | Kilde |
| --- | --- | --- |
| Tidsberegning | Serveren bruker egne tidspunkter. Første rapport gir null belastning. Neste rapport belaster intervallet hvis forrige status var ulåst og oppholdet er større enn null og høyst 63 sekunder. | `repository.go`: `insertHeartbeat` |
| Låsing og opplåsing | En låserapport kan avslutte og belaste et ulåst intervall. En opplåsingsrapport starter et nytt. Serveren kjenner mottakstidspunktet, ikke det nøyaktige tidspunktet Windows skiftet status. | `repository.go`, `accounting_regression_test.go` |
| Lengre opphold | Hele intervallet over 63 sekunder utelates. Tid blir ikke etterregistrert fra klientens aktivitetssekunder eller opprinnelige dato. | `repository.go`, `TestOfflineUsageIsNotChargedAfterLongShutdown` |
| Inaktivitet | Ulåst bruk teller også uten inndata. `idle` regnes som ulåst. | `repository.go`, `cmd/client/main_windows.go` |
| Flere PC-er | Forbruk summeres per bruker. Samtidig bruk på to enheter belaster begge. | `screen_time.go`: `forEachUsageInterval` |
| Regler | Pause overstyrer tidsplan, som overstyrer kvote. Null kvote betyr ubegrenset tid, men pause og tidsplan gjelder. Bonus gjelder kalenderdagen og overstyrer ikke pause eller tidsplan. | `policy.go`: `evaluatePolicy` |
| Rapportering | Klienten rapporterer ved statusendring og hvert 30. sekund. Den sender identitet, rapport-ID, klientversjon og `locked`. | `cmd/client/main_windows.go`, `cmd/client/protocol.go` |
| Duplikater | Samme rapport-ID gir ingen ny belastning eller oppdatering av tilstedeværelse. Serveren beregner likevel en ny beslutning fra gjeldende regler. | `repository.go`, `screen_time_service.go` |
| Lokal tilgang | Med en gyldig serverbeslutning kontrollerer klienten tillatelsens utløp, lokal restkvote og neste regelendring. Serveren gir høyst 90 sekunder; klientens egen øvre grense er 120. | `policy.go`, `cmd/client/state.go` |
| Første oppstart | Uten tidligere serverbeslutning tillates bruk mens klienten forsøker å få kontakt. | `cmd/client/state.go`: `allowed`, `loadState` |
| Kontaktfeil | Andre feil enn HTTP 401/403 setter klienten i en tilstand som tillater bruk uten tidsgrense. Dette kan oppheve en tidligere låsebeslutning. | `cmd/client/main_windows.go`, `markServerUnavailable` |
| Testmodus | Standard ved installasjon. Registrering fortsetter, men klienten kaller ikke Windows-låsing. Automatisk klientoppdatering beholder modusen. | `cmd/client/install.ps1`, `enforcement.go`, `update.ps1` |
| Stoppet klient | Ingen lokal telling eller låsing utføres mens prosessen er stoppet. Oppgaven er konfigurert for omstart ved feil, men det finnes ingen separat tjeneste som sikrer kontinuerlig håndheving. | `cmd/client/install.ps1`, `main_windows.go` |
| Oppdatering | Oppdatereren kontrollerer signatur, størrelse og filhash, bytter programfil og starter oppgaver som var i gang. Den sjekker at oppgavene fortsatt kjører etter åtte sekunder. Uendret fil gir ingen omstart. | `cmd/client/update.ps1` |
| Overvåking | `/monitor` viser de 100 siste loggede forespørslene fra autentiserte enheter, inkludert duplikater og valideringsfeil. Ingen innlogging kreves. Navn og nøkler vises ikke. | `heartbeat_monitor.go`, `api.go` |

## Viktigste funn, i prioritert rekkefølge

### 1. Reglene ved feil motsier hverandre

README, driftsveiledningen og API-kontrakten lover i praksis at siste tillatelse utløper, også ved nettbrudd. Den kjørende klientflyten kaller i stedet `markServerUnavailable`, som setter `ServerContacted=false`. `allowed` returnerer da `true` før kvote og utløp kontrolleres.

Dette er ikke bare en manglende test: `TestServerUnavailableFailsOpen` forventer uttrykkelig fri tilgang etter en tidligere låsebeslutning. Samtidig tester andre tester at en lagret tillatelse utløper. De siste går ikke gjennom nettfeilhåndteringen i hovedløkken.

Konsekvens: grønne tester kan bekrefte både kortvarige tillatelser og en feilovergang som omgår dem. Vi må velge én regel før dette endres.

### 2. «Ukjent», «ny klient» og «låst» blandes sammen

`ServerContacted=false` betyr både første oppstart og kontaktfeil. `invalidate` setter låsehandling, men endrer ikke dette flagget. Ved en ny klient eller etter kontaktfeil kan derfor en senere HTTP 401/403 sette `Action=lock` uten at `allowed` slutter å returnere `true`.

Dette følger av kodeflyten; det er ikke en observert hendelse på en PC. Eksisterende tester dekker ikke denne samlede overgangen gjennom hovedløkken.

Lagring og innlasting gir også ulik behandling: `loadState` tolker en lagret `server_unavailable`-tilstand som en tidligere serverbeslutning og setter flagget til `true`. Da kan tilgangen være stengt etter omstart, frem til neste kontaktfeil åpner den igjen.

Windows-avlesingen har en lignende sammenblanding: flere API-feil returnerer `locked`. Den verdien stopper serverens videre belastning og gjør at klienten ikke forsøker å låse økten. En avlesingsfeil er ikke bevis for at Windows er låst.

### 3. Klienten bærer fortsatt en eldre regnskapsmodell

`clientState` lagrer utestående aktivitetssekunder, aktivitetsdato og en rapport som venter på bekreftelse. `postHeartbeat` sender imidlertid ikke aktivitetssekunder eller aktivitetsdato. De lokale sekundene brukes fortsatt til å redusere lokal restkvote og varsler.

Denne lokale beregningen er altså delvis i bruk, men er ikke serverens regnskap. Det må komme tydelig frem før noe fjernes. Gamle tilstandsfiler og eldre klienter må håndteres bevisst.

### 4. Dokumentasjonen lover mer presis måling enn systemet kan gi

README sier at forsinket aktivitet belastes opprinnelig dato, og at `-idle-timeout` stopper opptellingen. Dette stemmer ikke med dagens serverberegning. API-dokumentasjonen beskriver disse delene riktigere.

Låsing, søvn og brukerbytte må også beskrives med målingens presisjon: serveren kjenner bare rapportene. Et langt opphold utelates, men et kort søvnopphold innenfor 63 sekunder kan inngå i et intervall dersom forrige rapport var ulåst. Det er ikke grunnlag for å love nøyaktig registrering av all faktisk skjermbruk.

### 5. Noen tester treffer kode som ikke brukes i produksjonsflyten

`blockedState` i `cmd/client/blocked.go` har egne tester, men brukes ikke av hovedløkken. Tilsvarende brukes `screenTimeDecision` bare av tester; faktiske beslutninger går gjennom `evaluatePolicy`.

Slike tester kan gi inntrykk av at en regel er verifisert i produktet. De bør erstattes av tester på den faktiske flyten, og ubrukt kode kan deretter fjernes.

### 6. Ansvarsfordelingen gjør endringer vanskelige å følge

Windows-klientens `main` håndterer flagg, konfigurasjon, tilstandslagring, rapporter, nettverksarbeid, programsporing, varsler og låsing. Mye av samspillet ligger i funksjoner som deler lokale variabler. De viktigste feilovergangene er dermed vanskelige å teste samlet.

På serveren blander `heartbeat_monitor.go` lagring, SQL-lesing, visningsmodeller, avviksmerking og HTTP. Normalisering av låsestatus finnes både der og i regnskapskoden. Overvåking introduserte også et internt felt i heartbeat-modellen for å skille manglende verdi fra null. Dette er konkrete kandidater for opprydding, uten at hele prosjektet trenger nye pakker eller et nytt rammeverk.

## Hva vi allerede kan bygge videre på

Policyberegningen er samlet i `policy.go`. Dags- og timevisning deler beregning av bruksintervaller. Eksisterende tester dekker blant annet låsing/opplåsing, duplikater, samtidige rapporter, midnatt, sommertid, kvoter, pause og bonus.

Oppdateringskontrollen og overvåkingen har også tester. Overvåkingsfeil er skilt fra tilgangssvaret: en loggfeil skal ikke endre serverens beslutning.

Men en fersk heartbeat med siste versjon bekrefter bare at den versjonen rapporterer. Den bekrefter ikke låsemodus, at Windows faktisk ble låst, eller at oppdatereren vil starte en allerede stoppet klient. Loggen over 100 rapporter er dessuten et kort feilsøkingsvindu, ikke full hendelseshistorikk.

## Forslag til regler som må avklares

Dette er et beslutningsgrunnlag, ikke en instruks om å implementere alt.

| Regel | Anbefalt utgangspunkt | Hva må avklares? |
| --- | --- | --- |
| Ett regnskap | Serveren eier belastet tid. Klientens lokale beregning er bare et estimat for tilgang og varsler frem til neste svar. | Beholde dagens måling fra rapportintervaller? Jeg anbefaler ja i første omgang. |
| Manglende kontakt | En nettfeil gir ingen ny tillatelse. Siste gyldige tillatelse gjelder bare til opprinnelig utløp. En eksplisitt lås beholdes. | Skal bruk stenges etter kort nettbrudd, eller er fortsatt spilling viktigere? Dette er et produktvalg. |
| Første oppstart | Bruk en kort, uttrykkelig oppstartsfrist uten tidligere tillatelse. Etter fristen gjelder regelen for manglende kontakt. | Hvor lang frist, og hvilken oppførsel ved første installasjon? For eksempel 30 sekunder må vurderes på testkonto. |
| Ukjent øktstatus | Skille `ukjent` fra bekreftet `låst`, med egen feilårsak. Ingen stille tolkning som låst. | Skal ukjent status midlertidig telles som ulåst, eller føre til stenging etter en frist? |
| Avvist enhetsnøkkel | HTTP 401/403 stenger tilgang i låsemodus, uansett tidligere tilstand. | Anbefales uten unntak for tidligere nettfeil eller ny klient. |
| Testmodus | Rapportering og beslutninger fungerer, men Windows låses aldri. | Beholde testmodus som standard, også ved ny installasjon/ordinær reinstallasjon? |
| Opphold i rapportering | Beholde 63-sekundersgrensen og ingen etterregistrering i første omgang. Vis tapte måleintervaller som usikkerhet. | Akseptere at ukjent aktivitet ikke kan belastes nøyaktig? |
| Stoppet prosess | Dagens klient kan ikke garantere håndheving etter at den er stoppet. | Skal beskyttelse mot avslutning bli et eget senere prosjekt med tjeneste og kontopolicy? Jeg anbefaler å behandle det separat. |

Begynn med valgene om nettbrudd, første oppstart og ukjent øktstatus. De påvirker hvilken tilstandsmodell klienten trenger. Kvoter, pause, bonus og ukeplan bør beholdes uendret under første opprydding.

## Plan: ett avgrenset steg om gangen

| Steg | Konkret leveranse | Ferdig når |
| --- | --- | --- |
| 1. Vedta oppførsel | En kort regelbeskrivelse med eksempler: normal bruk, låsing, kontaktfeil etter tillatelse, kontaktfeil etter lås, oppstart uten kontakt, avvist nøkkel og ukjent øktstatus. | Hver situasjon har ett forventet utfall. Ingen kode endres i dette steget. |
| 2. Sikre dagens faktiske flyt | Tester av klientens samlede feiloverganger, med falsk klokke, nettverk og Windows-handlinger. Gjør bare den minimale uttrekkingen som trengs for å teste dagens hovedløkke. | Dagens oppførsel er gjengitt, og forskjellene fra de vedtatte reglene er dokumentert. Ingen stille regelendring. |
| 3. Rydd ansvar | Skill oppstart/konfigurasjon, klientens hendelsesløp, tilgangstilstand og Windows-kall. Skill overvåkingens lagring fra presentasjon. Gjør dette i separate små endringer. | Samme observerbare oppførsel som før, med eksisterende tester og testene fra steg 2. |
| 4. Rett én regel om gangen | Start med valgt håndtering av kontaktfeil. Ta deretter avvist nøkkel/oppstart og ukjent øktstatus som egne endringer. | Testene dekker reelle overganger, og dokumentasjonen beskriver samme regel som koden. Hver endring kan vurderes og rulles tilbake separat. |
| 5. Fjern rester | Fjern ubrukt `blockedState` og `screenTimeDecision`. Forenkle gamle rapportfelter, klientlagring og databasefelt etter en eksplisitt vurdering av kompatibilitet. | Ingen aktiv kallsti eller nødvendig eldre dataformat er fjernet utilsiktet. Unødvendig struktur er faktisk redusert. |
| 6. Bekreft på Windows | Kjør en kort sjekkliste på egen standardkonto: låsing, opplåsing, nettbrudd, søvn, brukerbytte og oppdatering. | Logg, dashboard og faktisk Windows-oppførsel samsvarer. Først deretter rulles endret håndheving ut til barnas PC-er. |

Hvert steg er en egen arbeidsoppgave. Planen gir ikke klarsignal til å utføre alle stegene nå.

Arbeidsdokument for steg 1: [Regler for telling og tilgang](BEHAVIOR_RULES.md). Brukeren har senere valgt at siste gyldige restkvote skal gjelde uten nett, med lokal beregning. Ved gjenoppkobling mottar serveren oppdatert restkvote og overtar som master igjen; forbruk fra flere enheter avstemmes uten dobbeltbelastning. Lagret ukeplan, pause og tidsplan håndheves lokalt, og mulig overforbruk mellom frakoblede PC-er aksepteres. Uten lagrede regler forsøkes kontakt umiddelbart ved oppstart og hver gjenopplåsing; hvis regler ikke kan hentes, låses økten igjen. Dette erstatter de opprinnelige anbefalingene om 90-sekunders utløp og oppstartsfrist. Ved avlesingsfeil er det vedtatt å samle observasjoner i klientlogg og overvåking først, uten ny regel for telling eller automatisk låsing etter 30 sekunder. Langsiktig feiloppførsel er uttrykkelig utsatt til vi har observasjoner. Steg 1 er fullført med denne avgrensningen. Steg 2 er også fullført: [dagens klientflyt og dokumenterte avvik](STEP_2.md) beskriver testgrunnlaget uten endring av produktreglene. Beskrivelsen av dagens kode ovenfor er uendret.

## Arbeidsregler for oppryddingen

- Én kilde beskriver produktreglene; API, drift og README viser til den og må holdes i samsvar.
- Skill rene strukturendringer fra endret oppførsel. Beskriv konkrete før/etter-eksempler ved regelendringer.
- Bruk tydelige tilstander for tilgang, forbindelsesstatus og Windows-økt. Ett boolsk flagg skal ikke bety flere forskjellige ting.
- Test den aktive flyten. Tester av ubrukt hjelpekode teller ikke som verifisering av produktet.
- Behold kompatibilitet med installerte klienter og lagrede tilstandsfiler frem til en eksplisitt overgang er bestemt.
- Del opp etter ansvar der det gjør flyten lettere å lese; unngå nye lag og grensesnitt uten et konkret behov.
- Oppdater klientversjonen når klientoppførsel endres, og beskriv hvordan endringen kontrolleres og eventuelt tilbakeføres.

## Kontroller utført i denne gjennomgangen

`go test ./...` og `go vet ./...` bestod 24. oktober 2026. Testene kjører mot testdata; ingen klient ble installert eller startet. Ingen levende PC, produksjonsdatabase eller Windows-håndheving er undersøkt. Ingen nye regresjonstester eller feilrettinger er lagt til i denne fasen.
