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
| [state.go](../cmd/client/state.go) | Holder lokal resttid, siste serverbeslutning og ventende rapport; leser og skriver tilstandsfilen. |
| [activity.go](../cmd/client/activity.go) | Måler lokale sekunder mellom sjekkene. |
| [session_windows.go](../cmd/client/session_windows.go) | Leser Windows-økten gjennom Windows-API-er. |
| [enforcement.go](../cmd/client/enforcement.go) | Utfører låseforsøk eller registrerer at den ville ha låst i testmodus. |
| [warning.go](../cmd/client/warning.go) | Vurderer når et tidsvarsel skal vises. |

`clientLoop` samler verdiene som løkken må huske mellom hendelser. `state` er den lagrede tilstanden, mens for eksempel `heartbeatInFlight` bare forteller at et nettverkssvar venter. `clientRuntime` inneholder funksjonene og tidsurene som Windows-oppstarten stiller til rådighet. Testene bruker samme løkke med simulerte ytre handlinger.

Nederst i `runtime.go` ligger metodene for valgfri programsporing. De endrer ikke kvoten. De kan leses separat fra hovedflyten for tid og tilgang.

## Status for oppryddingen

To avgrensede deler av steg 3 er gjennomført. Først ble den store løkkefunksjonen delt i navngitte metoder. Deretter ble lesing av argumenter, sammensetting av konfigurasjon og valg av låsemodus flyttet fra `main` til `config.go`. Hovedprogrammet viser nå oppstartsrekkefølgen, og hendelsesløkken viser kjørerekkefølgen. Testene fra steg 2 er beholdt uten endringer.

Dette er en strukturendring. Oppførselen og de kjente avvikene i [steg 2-rapporten](STEP_2.md) er fortsatt testgrunnlaget. Eksempelvis åpner `handleHeartbeatError` fortsatt tilgangen ved vanlige kontaktfeil; rettingen av dette skal følge de [vedtatte reglene](BEHAVIOR_RULES.md) i en egen endring.

Innstillingskildenes prioritet og valget av testmodus/låsemodus kontrolleres også i `config_test.go`, med vanlige filer og kommandolinjeargumenter. Testene simulerer ingen Windows-låsing.

Kontrollert med `go test ./cmd/client`, `go test -race ./...`, `go vet ./...` og Windows-bygg til `.artifacts/screengate-client-step3-config.exe`. Programmet er ikke installert eller startet gjennom dette arbeidet.

Videre opprydding av tilstandslagring og serverens overvåkingskode er ikke utført i disse delene.
