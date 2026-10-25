# Slik leser du Windows-klienten

Start med `runClient` i [runtime.go](../cmd/client/runtime.go). Den starter rapporteringen og venter deretter på fem typer hendelser:

| Hendelse | Metode | Hva den gjør |
| --- | --- | --- |
| Ett sekund er gått | `poll` | Leser Windows-status, teller lokal bruk, lagrer, vurderer varsler og sjekker om økten skal låses. |
| Tretti sekunder er gått | `report` | Sender heartbeat og eventuelle valgfritt aktiverte programrapporter. |
| Heartbeat har fått svar eller feilet | `handleHeartbeat` | Behandler resultatet, lagrer tilstand og vurderer låsing. |
| En valgfri programrapport er ferdig | `handleEvent` | Fjerner rapporten fra køen hvis sendingen lyktes. |
| Klienten avsluttes | `stop` | Avslutter lokal måling og lagrer tilstanden. |

Det er disse metodene du følger for å forstå klientens hovedflyt. Nettverkssendingen skjer i bakgrunnen, og resultatet leveres tilbake til denne løkken. Dermed kan klienten fortsette å sjekke tid og låsing mens den venter på nettverket.

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
| [main_windows.go](../cmd/client/main_windows.go) | Starter programmet, leser oppsett og kobler til de faktiske Windows-funksjonene og tidsurene. |
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

Dette er første avgrensede del av steg 3. Den store funksjonen med lokale hjelpefunksjoner er delt i navngitte metoder. Hendelsesløkken viser nå hovedflyten, og detaljene kan leses én metode om gangen. Testene fra steg 2 er beholdt uten endringer.

Dette er en strukturendring. Oppførselen og de kjente avvikene i [steg 2-rapporten](STEP_2.md) er fortsatt testgrunnlaget. Eksempelvis åpner `handleHeartbeatError` fortsatt tilgangen ved vanlige kontaktfeil; rettingen av dette skal følge de [vedtatte reglene](BEHAVIOR_RULES.md) i en egen endring.

Kontrollert med `go test ./cmd/client`, `go test -race ./...`, `go vet ./...` og Windows-bygg til `.artifacts/screengate-client-step3.exe`. Programmet er ikke installert eller startet gjennom dette arbeidet.

Videre opprydding av konfigurasjon, tilstandslagring og serverens overvåkingskode er ikke utført i denne delen.
