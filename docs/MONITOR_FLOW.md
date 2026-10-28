# Fra heartbeat til overvåkingssiden

Overvåkingen har to hovedløp: lagre en mottatt heartbeat og vise den lagrede historikken. Du kan lese dem hver for seg. Tidsberegning og tilgangsbeslutning skjer i den eksisterende heartbeat-behandlingen; overvåkingen registrerer resultatet.

## Når en heartbeat kommer inn

1. `heartbeatHandler` i [api.go](../api.go) behandler forespørselen og sender serverens svar.
2. `observeHeartbeat` i [heartbeat_monitor.go](../heartbeat_monitor.go) registrerer resultatet for den autentiserte enheten. Forespørsler uten gyldig enhetsautentisering går ikke inn i loggen.
3. `monitorRecordFromHeartbeat` lager posten som skal lagres. Den begrenser klientversjonen til et forventet versjonsformat og skiller manglende aktivitetssekunder fra verdien null. Ved feil lagres ingen oppdiktet kvotebeslutning.
4. `saveMonitor` i [heartbeat_monitor_repository.go](../heartbeat_monitor_repository.go) oppdaterer enhetsstatus, legger til rapporten og beholder de 100 siste postene samlet. Dette skjer i én databasetransaksjon.

En feil i overvåkingslagringen skrives til serverloggen. Den endrer ikke tilgangssvaret som heartbeat-behandlingen gir klienten.

## Når du åpner `/monitor`

1. `monitorHandler` leser og kontrollerer enhetsfilteret fra URL-en.
2. `monitorData` henter enheter og rapporter gjennom databasemetodene `monitorDevices` og `monitorEntries`.
3. `monitorDeviceView` og `monitorEntryView` i [heartbeat_monitor_views.go](../heartbeat_monitor_views.go) gjør tall og tidspunkter om til visningstekst.
4. `monitorNote` bestemmer merknaden og om rapporten skal fremheves. Den beholder prioriteten mellom duplikat, avvik, feil og langt rapportopphold.
5. `monitorHandler` bruker den eksisterende HTML-malen og sender nettsiden med automatisk oppdatering hvert tiende sekund.

Databasemetodene returnerer tall, tidspunkter og rapporterte tilstander. De bestemmer ikke norske etiketter, tidsformatering eller gul markering. Visningsfunksjonene har ingen databasespørringer.

## Filenes ansvar

| Fil | Ansvar |
| --- | --- |
| [heartbeat_monitor.go](../heartbeat_monitor.go) | Registrere en mottatt rapport, klargjøre data og håndtere HTTP for siden. |
| [heartbeat_monitor_repository.go](../heartbeat_monitor_repository.go) | Lagring, henting, rapportopphold og grensen på 100 poster. |
| [heartbeat_monitor_views.go](../heartbeat_monitor_views.go) | Visningsmodeller, formatering, statusetiketter og merknader. |
| [templates/monitor.html](../templates/monitor.html) | Oppsettet og teksten på selve nettsiden. |

Den offentlige siden viser anonyme aliaser som «Enhet 1». Brukernavn, maskinnavn og enhetsnøkler blir ikke felter i visningsmodellene. Et manglende tall vises som `—`; en ubegrenset dagskvote vises som «Ubegrenset».

## Kontroll av oppryddingen

Denne delen endrer strukturen, ikke tabellene, HTML-malen, endepunktet eller hvilke rapporter som vises. De eksisterende overvåkingstestene er beholdt uendret. De kontrollerer blant annet filter, 100-postgrensen, duplikater, belastning, avviste forespørsler, skjerming av identiteter og at overvåkingsfeil ikke endrer tilgangssvaret.

Visningsdata er også sammenlignet direkte med den tidligere `monitorData`-funksjonen ved en midlertidig kontroll. Kontrollen omfattet manglende verdier, null, ubegrenset kvote, avvik rundt tosekundersgrensen, rapportopphold, duplikater, HTTP-feil og flere enhetsfiltre. Resultatene var identiske. Referansekopien ligger bare under ignorerte `.artifacts`, ikke som en ekstra implementasjon i programmet.

`go test ./...`, `go test -race ./...`, `go vet ./...` og serverbygg til `.artifacts/screengate-step3-monitor.exe` er kontrollene for denne delen. Det er ikke gjort noen utrulling.

## Status i planen

Den planlagte ansvarsdelingen i steg 3 er nå gjennomført: klientens hovedløp, oppstartsinnstillinger og tilstandslagring er ryddet, og overvåkingens lagring er skilt fra presentasjon. [Klientveiledningen](CLIENT_FLOW.md) beskriver klientdelen.

Steg 4 er en egen oppgave: innføre én av de [vedtatte reglene](BEHAVIOR_RULES.md) om gangen og rette de dokumenterte avvikene. Dette er ikke gjort gjennom strukturendringene.
