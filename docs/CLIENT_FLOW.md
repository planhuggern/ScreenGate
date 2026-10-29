# Slik leser du Windows-klienten

Start med `runClient` i [runtime.go](../cmd/client/runtime.go). Den starter rapporteringen og venter deretter på fem typer hendelser:

| Hendelse | Metode | Hva den gjør |
| --- | --- | --- |
| Ett sekund er gått | `poll` | Leser Windows-status, teller lokal bruk, lagrer, vurderer varsler og sjekker om økten skal låses. |
| Tretti sekunder er gått | `report` | Sender heartbeat og eventuelle valgfritt aktiverte programrapporter. |
| Heartbeat har fått svar eller feilet | `handleHeartbeat` | Behandler resultatet, lagrer tilstand og vurderer låsing. |
| Svar på en programrapport | `handleEvent` | Fjerner rapporten fra køen hvis sendingen lyktes. Dette brukes når programsporing er slått på. |
| Klienten avsluttes | `stop` | Avslutter lokal måling og lagrer tilstanden. |

Det er disse metodene du følger for å forstå klientens hovedflyt. Nettverkssendingen skjer i bakgrunnen, og resultatet leveres tilbake til denne løkken. Dermed kan klienten fortsette å sjekke tid og låsing mens den venter på nettverket.

En programrapport forteller hvilket program som var i forgrunnen, og hvor lenge. Programsporing er én eksisterende funksjon som slås på med `-track-apps`; den er avslått som standard og brukes ikke til tidsberegning. Serveren lagrer foreløpig ikke innholdet. Funksjonen beholdes for senere bruk.

## Før løkken starter: innstillinger og oppstart

`main` i [main_windows.go](../cmd/client/main_windows.go) følger denne rekkefølgen:

1. Sett opp logging.
2. Les kommandolinjeargumentene med `readClientOptions`.
3. Hvis varseltesting er valgt med `-debug-remaining`, kjør den og avslutt.
4. Last og kontroller innstillingene med `loadClientConfig`.
5. Velg testmodus eller låsemodus med `lockingEnabled`.
6. Kontroller at klienten ikke allerede kjører, og last lagret klienttilstand.
7. Koble til Windows-funksjoner og tidsur, og kall `runClient`.

De tre funksjonene for innstillinger ligger samlet i [config.go](../cmd/client/config.go). Det er ikke lagt til nye innstillinger. Ved vanlig installasjon brukes konfigurasjonsfilen som installasjonsskriptet oppretter.

Når innstillinger oppgis flere steder, beholdes den eksisterende rekkefølgen:

| Kilde | Hvordan den brukes |
| --- | --- |
| Miljøvariabelen `SCREENGATE_DEVICE_TOKEN` | Kan gi enhetsnøkkel ved manuell oppstart uten konfigurasjonsfil. |
| Filen oppgitt med `-config` | Erstatter grunnkonfigurasjonen, også miljøvariabelens nøkkel. Manglende nøkkel i filen gir ikke automatisk tilbakefall til miljøvariabelen. |
| Eksplisitte argumenter | Server, bruker, enhet og nøkkel overstyrer verdiene fra filen når argumentene er oppgitt. |
| Filen oppgitt med `-token-file` | Overstyrer også en nøkkel oppgitt med `-token`. En fil som ikke kan leses gir feil. |
| Windows-identitet | Brukes som standard for bruker og maskinnavn hvis de fortsatt mangler. Serveradresse og enhetsnøkkel må finnes og kontrolleres før klienten starter. |

Låsing er aktivert hvis installasjonsfilen eller `-enable-locking` ber om det. `-test-mode` overstyrer begge og hindrer Windows-låsing. Uten aktivering av låsing brukes testmodus. Innstillingene for programsporing og inaktivitet sendes videre til løkken som før.

## Tilstanden mellom kjøringer

[state.go](../cmd/client/state.go) beskriver hva klienten husker og hvordan verdiene endres ved lokal bruk og serversvar. [state_store.go](../cmd/client/state_store.go) håndterer lesing, kontroll og skriving av filen. Du kan dermed lese tilgangsreglene uten samtidig å følge filoperasjoner og signering.

Ved oppstart følger `loadState` denne rekkefølgen:

1. Åpne filen. En manglende fil gir dagens starttilstand for en ny klient.
2. Kontroller filstørrelsen.
3. Les JSON og kontroller signaturen med `readSignedState`.
4. Kontroller identitet og tillatte verdier med `validateSavedState`.
5. Gjenopprett kompatibilitetsfelt og kontroller klokken med `restoreAfterLoad` i `state.go`.

En ødelagt eller ugyldig fil returnerer dagens stengte reservetilstand og en feil. Dette er forskjellig fra en fil som ikke finnes. Den videre behandlingen av denne tilstanden i klientløkken er fortsatt beskrevet av testene fra steg 2.

`saveState` skriver tilstanden som JSON med en signatur. Den skriver først en midlertidig fil, synkroniserer og lukker den, og erstatter deretter tilstandsfilen. Filnavn, JSON-felt, signering og denne skriverekkefølgen er beholdt.

Kompatibilitetstesten i `state_store_test.go` leser en fast eksempel-fil fra før feltet `server_contacted` ble innført. Den kontrollerer at resttid, utløp og ventende rapport bevares. Eksisterende tester kontrollerer blant annet omstart, feil nøkkel, endrede data og den kjente forskjellen i håndheving før og etter omstart under nettbrudd.

## Eksempel: barnet låser opp Windows

1. `poll` leser den nye Windows-statusen.
2. Den lokale måleren oppdaterer forbruket siden forrige sjekk.
3. Når statusen har endret seg, starter `sendHeartbeat` en rapport til serveren.
4. `handleHeartbeat` kjøres når svaret kommer tilbake.
5. Et gyldig serversvar går til `applyServerResponse`. En feil går til `handleHeartbeatError`.
6. Tilstanden lagres, og `enforce` vurderer om klienten skal forsøke Windows-låsing.

Hvis en rapport allerede venter på svar, starter ikke `sendHeartbeat` enda en parallell rapport. Etter svaret kontrolleres det om øktstatusen har endret seg i mellomtiden, og rapportering forsøkes igjen ved behov.

## Hvilken kode har hvilket ansvar?

| Fil | Ansvar |
| --- | --- |
| [main_windows.go](../cmd/client/main_windows.go) | Bestemmer oppstartsrekkefølgen og kobler til de faktiske Windows-funksjonene og tidsurene. |
| [config.go](../cmd/client/config.go) | Leser eksisterende argumenter og konfigurasjonsfil, bestemmer hvilken innstillingskilde som gjelder og kontrollerer resultatet. |
| [runtime.go](../cmd/client/runtime.go) | Bestemmer rekkefølgen: les status, mål bruk, send rapporter, håndter svar, lagre og vurder låsing. |
| [protocol.go](../cmd/client/protocol.go) | Lager HTTP-forespørsler og kontrollerer serversvar. |
| [state.go](../cmd/client/state.go) | Holder lokal resttid, siste serverbeslutning og ventende rapport, og bestemmer hvordan de endres. |
| [state_store.go](../cmd/client/state_store.go) | Leser, kontrollerer, signerer og skriver tilstandsfilen i eksisterende format. |
| [usage.go](../cmd/client/usage.go) | Samler målt bruk, beholder forbruksrapporten til bekreftelse og beregner utestående bruk per dag. |
| [local_policy.go](../cmd/client/local_policy.go) | Følger lagret ukeplan og starter ny dagskvote ved midnatt uten å fylle opp dagens restkvote. |
| [rules.go](../internal/screenpolicy/rules.go) | Kalenderreglene som både server og klient bruker for ukedager, tidsvinduer og bonusdato. |
| [activity.go](../cmd/client/activity.go) | Måler lokale sekunder mellom sjekkene. |
| [session_windows.go](../cmd/client/session_windows.go) | Leser Windows-økten gjennom Windows-API-er. |
| [session_reader.go](../cmd/client/session_reader.go) | Følger leserekkefølgen og returnerer brukt tilstand sammen med eventuell avlesingsfeil. |
| [session_diagnostics.go](../cmd/client/session_diagnostics.go) | Logger feil og gjenoppretting, og beholder siste feil/vellykkede avlesing til rapportering. |
| [enforcement.go](../cmd/client/enforcement.go) | Utfører låseforsøk eller registrerer at den ville ha låst i testmodus. |
| [warning.go](../cmd/client/warning.go) | Vurderer når et tidsvarsel skal vises. |

`clientLoop` samler verdiene som løkken må huske mellom hendelser. `state` er den lagrede tilstanden, mens for eksempel `heartbeatInFlight` bare forteller at et nettverkssvar venter. `clientRuntime` inneholder funksjonene og tidsurene som Windows-oppstarten stiller til rådighet. Testene bruker samme løkke med simulerte ytre handlinger.

Nederst i `runtime.go` ligger metodene for valgfri programsporing. De endrer ikke kvoten. De kan leses separat fra hovedflyten for tid og tilgang.

## Status for oppryddingen

Tre avgrensede deler av steg 3 er gjennomført. Først ble den store løkkefunksjonen delt i navngitte metoder. Deretter ble oppstartsinnstillingene samlet i `config.go`. Til slutt ble lesing, kontroll og skriving av tilstandsfilen skilt ut i `state_store.go`, mens tilstandsreglene ble igjen i `state.go`. Hovedprogrammet viser oppstartsrekkefølgen, og hendelsesløkken viser kjørerekkefølgen. Testene fra steg 2 er beholdt uten endringer.

Steg 3 var strukturendringer med testene fra [steg 2-rapporten](STEP_2.md) som grunnlag. Senere er regelendringer innført i [steg 4](STEP_4.md): `handleHeartbeatError` bevarer siste beslutning ved kontaktfeil, og `allowed` håndhever lokal restkvote og kjent stengetid. `ServerUnavailable` holder kontaktfeil adskilt fra om en tidligere gyldig beslutning finnes. Testene for de rettede avvikene er oppdatert; resten av de [vedtatte reglene](BEHAVIOR_RULES.md) innføres videre i egne endringer.

Fra `2026.10.28.2` går lokalt målte intervaller også gjennom `usage.go`. `prepareUsage` lager et øyeblikksbilde med en stabil ID. `prepareReport` legger dette ved heartbeaten. `protocol.go` krever at serveren bekrefter samme ID, og `apply` fjerner først da det bekreftede forbruket. Nyere bruk forblir i køen og trekkes fortsatt lokalt. Denne køen og rapporten lagres sammen med resten av tilstanden, slik at et mistet svar eller en omstart ikke mister data. Ved gjenoppkobling sender løkken resten av den oppsamlede køen før den går tilbake til vanlig rapportrytme.

Fra `2026.10.28.3` går kalenderreglene gjennom `local_policy.go`. `refreshPolicy` viderefører dagens restkvote, men gir riktig ny dagskvote når serverens kalenderdato skifter. `rememberDailyUsage` fordeler målt bruk mellom datoer, slik at et sent svar over midnatt ikke gir brukt tid tilbake. Ukeplan, pause og datobundet bonus kommer fra serverens `policy`-objekt. Selve tolkningen av ukeplanen ligger i `internal/screenpolicy`, felles for klient og server. Oppdagede lokale feil holdes separat i `LocalBlockReason`, slik at en åpning i ukeplanen ikke opphever for eksempel en nøkkelavvisning.

Fra `2026.10.28.4` gir Windows-avlesingen også eventuell feilinformasjon. `observeSession` i `session_diagnostics.go` logger feil og gjenoppretting, og oppdaterer diagnostikken som følger heartbeats. Tilstanden som går til måleren og låsevurderingen er fortsatt den samme som før: de fleste avlesingsfeil gir reserveverdien `locked`, mens feil i inaktivitetsavlesingen gir `active`. Feilopplysningene alene innfører ingen ny håndheving. Siste feil beholdes også etter gjenoppretting, slik at en kort feil under nettbrudd ikke forsvinner før neste rapport.

[Steg 5](STEP_5.md) fjerner den ubrukte `blockedState` og dens isolerte tester. Klientløkken bruker fortsatt `clientState`. Eldre filfelt og kompatibilitetsbehandling er beholdt etter en egen vurdering i rapporten; ingen telle- eller låseregel endres gjennom denne oppryddingen.

Innstillingskildenes prioritet og valget av testmodus/låsemodus kontrolleres også i `config_test.go`, med vanlige filer og kommandolinjeargumenter. Testene simulerer ingen Windows-låsing.

Kontrollert med `go test ./cmd/client`, `go test -race ./...`, `go vet ./...` og Windows-bygg til `.artifacts/screengate-client-step3-state.exe`. Programmet er ikke installert eller startet gjennom dette arbeidet.

Serverens overvåkingskode er senere ryddet i en egen del: [Fra heartbeat til overvåkingssiden](MONITOR_FLOW.md). Den planlagte ansvarsdelingen i steg 3 er dermed gjennomført. Nye tilgangsregler og offline-avstemming er ikke innført gjennom strukturendringene.
